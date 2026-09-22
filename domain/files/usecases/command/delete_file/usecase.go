package deletefile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"path"
	"slices"
	"strings"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/goshared/pkg/filesanitize"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	eventstream "github.com/assurrussa/goshared/services/event-stream"
	outboxtypes "github.com/assurrussa/outbox/shared/types"

	"github.com/assurrussa/gouploads/domain/files/model"
	eventfileafterprocess "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process"
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

//go:generate toolsmocks

type fileRepository interface {
	GetByID(ctx context.Context, id int64) (model.File, error)
	DeleteByID(ctx context.Context, id int64) error
}

type fileStorage interface {
	Delete(ctx context.Context, relativePath string) error
	DeleteBatch(ctx context.Context, relativePaths []string) error
}

type transactor interface {
	RunInTx(ctx context.Context, f func(context.Context) error) error
}

type outboxPutter interface {
	Put(ctx context.Context, name, payload string, availableAt time.Time) (outboxtypes.JobID, error)
}

//go:generate options-gen -out-filename=usecase_options.gen.go -from-struct=Options
type Options struct {
	transactor      transactor              `option:"mandatory" validate:"required"`
	files           fileRepository          `option:"mandatory" validate:"required"`
	eventStream     eventstream.EventStream `option:"mandatory" validate:"required"`
	logger          logger.Logger           `option:"mandatory" validate:"required"`
	storage         fileStorage             `option:"mandatory" validate:"required"`
	outbox          outboxPutter            `option:"mandatory" validate:"required"`
	deliveryBaseURL string                  `validate:"omitempty,url"`
}

type UseCase struct {
	Options
}

var ErrFileOwnershipMismatch = errors.New("file does not belong to the expected object")

func Must(opts Options) *UseCase {
	useCase, err := New(opts)
	if err != nil {
		panic(err)
	}
	return useCase
}

func New(opts Options) (*UseCase, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate options: %w", err)
	}

	return &UseCase{
		Options: opts,
	}, nil
}

func (u *UseCase) Handle(ctx context.Context, req Request) (resp Response, errReturn error) {
	if err := req.Validate(); err != nil {
		return Response{}, fmt.Errorf("validate: %w", err)
	}

	var filePath string
	var err error
	if req.FilePath != "" {
		filePath, err = filesanitize.EnsureRelativePath(req.FilePath)
		if err != nil {
			return Response{}, fmt.Errorf("ensure relative path: %w", err)
		}
	}

	var file model.File
	if req.FileID > 0 {
		file, err = u.files.GetByID(ctx, req.FileID)
		if err != nil {
			return Response{}, fmt.Errorf("get task %d: %w", req.FileID, err)
		}
	} else if filePath != "" {
		file = model.File{
			FolderPath: path.Dir(filePath),
			FileName:   path.Base(filePath),
		}
	}
	if file.ID > 0 {
		//nolint:ineffassign,staticcheck,wastedassign,nolintlint // Если есть файл, то и путь надо брать от него, а не от реквеста.
		filePath = file.GetFullPath()
	}
	if err := validateOwnership(file, req); err != nil {
		return Response{}, err
	}

	deletePaths := collectPaths(file, filePath)

	taskLogger := u.logger.WithAttrs(
		slog.Int64("file_id", req.FileID),
		slog.Int64("deleted_id", req.FileID),
		slog.String("file_path", filePath),
	)
	if req.FileID == 0 {
		taskLogger = taskLogger.WithAttrs(slog.String("staging_key", filePath))
	} else {
		taskLogger = taskLogger.WithAttrs(slog.Any("public_keys", deletePaths))
	}
	taskLogger.InfoContext(ctx, "deleting media objects")

	defer func() {
		if errReturn == nil || req.FileID == 0 {
			return
		}

		taskLogger.ErrorContext(ctx, "failed deleted file", logger.Error(errReturn))
		u.publish(ctx, req.UserID, u.buildFailedEvent(file, errReturn))
	}()

	switch file.ID {
	case 0:
		// Если файл в итоге будет не найден в БД - можно будет попробовать просто удалить файл из S3
		if err := u.removeFile(ctx, file, filePath); err != nil {
			return Response{}, fmt.Errorf("remove file: %w", err)
		}
	default:
		err = u.transactor.RunInTx(ctx, func(ctx context.Context) error {
			if err := u.files.DeleteByID(ctx, file.ID); err != nil {
				return fmt.Errorf("delete old file record: %w", err)
			}

			if err := u.enqueueAfterJobs(ctx, file.ID, file, req.AfterEvents); err != nil {
				return fmt.Errorf("schedule after jobs: %w", err)
			}

			return u.removeFile(ctx, file, filePath)
		})
		if err != nil {
			return Response{}, fmt.Errorf("trx: %w", err)
		}
	}

	if req.FileID > 0 {
		u.publish(ctx, req.UserID, u.buildCompletedEvent(file))
	}

	return Response{}, nil
}

func (u *UseCase) publish(ctx context.Context, userID sharedtypes.UserID, event shared.FileDeletedEvent) {
	if err := event.Validate(); err != nil {
		u.logger.WarnContext(ctx, "invalid event", logger.Error(err))
		return
	}

	if userID.IsZero() {
		u.logger.WarnContext(ctx, "skip publish: empty user id")
		return
	}

	if err := u.eventStream.Publish(ctx, userID, event); err != nil {
		u.logger.WarnContext(ctx, "publish event", logger.Error(err))
	}
}

func (u *UseCase) enqueueAfterJobs(
	ctx context.Context,
	fileID int64,
	file model.File,
	events []shared.FileEventAfterJob,
) error {
	for _, event := range events {
		jobName := strings.TrimSpace(event.JobName)
		if jobName == "" {
			continue
		}

		meta := make(map[string]any, len(event.Meta)+5)
		if file.ObjectType.Validate() == nil {
			meta["objectType"] = file.ObjectType.String()
		}
		if file.ObjectID != nil {
			meta["objectId"] = file.ObjectID
		}
		if file.URL != "" {
			meta["fileUrl"] = file.URL
		}
		if file.FileName != "" {
			meta["fileName"] = file.FileName
		}
		if file.OriginalFileName != "" {
			meta["originalFileName"] = file.OriginalFileName
		}

		if len(event.Meta) > 0 {
			for k, v := range event.Meta {
				meta[k] = v
			}
		}

		payload := eventfileafterprocess.NewPayload(event.UserID, shared.UserTypeAdmin, fileID, jobName, meta)
		payloadBytes, err := eventfileafterprocess.MarshalPayload(payload)
		if err != nil {
			return fmt.Errorf("marshal after job payload: %w", err)
		}

		if _, err := u.outbox.Put(ctx, jobName, payloadBytes, time.Now()); err != nil {
			return fmt.Errorf("outbox put: %w", err)
		}
	}

	return nil
}

func (u *UseCase) removeFile(ctx context.Context, file model.File, fallbackPath string) error {
	paths := collectPaths(file, fallbackPath)
	if len(paths) == 0 {
		return nil
	}

	if len(paths) == 1 {
		if err := u.storage.Delete(ctx, paths[0]); err != nil && !errors.Is(err, filestorage.ErrNotSupported) {
			return fmt.Errorf("storage delete file: %w", err)
		}
		return nil
	}

	if err := u.storage.DeleteBatch(ctx, paths); err != nil {
		if errors.Is(err, filestorage.ErrNotSupported) {
			for _, p := range paths {
				if err := u.storage.Delete(ctx, p); err != nil && !errors.Is(err, filestorage.ErrNotSupported) {
					return fmt.Errorf("storage delete file: %w", err)
				}
			}
			return nil
		}

		return fmt.Errorf("storage delete batch: %w", err)
	}

	return nil
}

func collectPaths(file model.File, fallback string) []string {
	paths := make([]string, 0, len(file.GetData().Presets)+2)
	unique := make(map[string]struct{}, cap(paths))
	add := func(pathValue string) {
		pathValue = strings.TrimSpace(pathValue)
		if pathValue == "" {
			return
		}
		if _, exists := unique[pathValue]; exists {
			return
		}
		unique[pathValue] = struct{}{}
		paths = append(paths, pathValue)
	}

	add(fallback)

	if file.ID == 0 {
		return paths
	}

	add(file.GetFullPath())

	data := file.GetData()
	if data == nil || len(data.Presets) == 0 {
		return paths
	}

	for presetName, preset := range data.Presets {
		add(preset.RelativePath)

		name := strings.TrimSpace(preset.PresetName)
		if name == "" {
			name = presetName.String()
		}
		if name == "" || shared.PresetName(name) == shared.FilePresetMainName {
			continue
		}

		add(path.Join(file.FolderPath, name, file.FileName))
	}

	slices.Sort(paths)
	return paths
}

func validateOwnership(file model.File, req Request) error {
	if req.ObjectType == "" && req.ObjectID == 0 {
		return nil
	}
	if file.ID == 0 {
		return nil
	}
	if file.ObjectID == nil || file.ObjectType != req.ObjectType || *file.ObjectID != req.ObjectID {
		return fmt.Errorf(
			"%w: file_id=%d expected_object_type=%s expected_object_id=%d",
			ErrFileOwnershipMismatch,
			req.FileID,
			req.ObjectType,
			req.ObjectID,
		)
	}

	return nil
}

func (u *UseCase) buildCompletedEvent(file model.File) shared.FileDeletedEvent {
	return shared.NewFileDeletedEvent(file.ID, u.publicURL(file), shared.FileDeleteStatusCompleted)
}

func (u *UseCase) buildFailedEvent(file model.File, err error) shared.FileDeletedEvent {
	event := shared.NewFileDeletedEvent(file.ID, u.publicURL(file), shared.FileDeleteStatusFailed)
	event.Error = err.Error()
	return event
}

func (u *UseCase) publicURL(file model.File) string {
	return fileurl.Compose(u.deliveryBaseURL, "", file.GetPublicURL())
}
