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
	outboxtypes "github.com/assurrussa/outbox/shared/types"

	"github.com/assurrussa/gouploads/domain/files/model"
	eventfileafterprocess "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process"
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	eventstream "github.com/assurrussa/gouploads/internal/events"
	"github.com/assurrussa/gouploads/internal/filesanitize"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

//go:generate toolsmocks

type fileRepository interface {
	GetByIDForUpdate(ctx context.Context, id int64) (model.File, error)
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
	transactor      transactor            `option:"mandatory" validate:"required"`
	files           fileRepository        `option:"mandatory" validate:"required"`
	eventStream     eventstream.Publisher `option:"mandatory" validate:"required"`
	logger          logger.Logger         `option:"mandatory" validate:"required"`
	storage         fileStorage           `option:"mandatory" validate:"required"`
	outbox          outboxPutter          `option:"mandatory" validate:"required"`
	deliveryBaseURL string                `validate:"omitempty,url"`
}

type UseCase struct{ Options }

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
	return &UseCase{Options: opts}, nil
}

func (u *UseCase) Handle(ctx context.Context, req Request) (Response, error) {
	if err := req.Validate(); err != nil {
		return Response{}, fmt.Errorf("validate: %w", err)
	}
	if req.FileID == 0 {
		key, err := filesanitize.EnsureRelativePath(req.FilePath)
		if err != nil {
			return Response{}, fmt.Errorf("ensure relative path: %w", err)
		}
		// A staging-only job has no database record to transition.
		return Response{}, u.deletePaths(ctx, []string{key})
	}
	planner, ok := u.files.(deletionPlanner)
	if !ok {
		return Response{}, errors.New("file deletion requires a durable deletion-plan repository")
	}
	plan, found, err := u.prepareDeletion(ctx, req, planner)
	if err != nil {
		return Response{}, err
	}
	if !found || plan.Completed {
		return Response{}, nil
	}

	u.logger.InfoContext(ctx, "purging committed file deletion",
		slog.Int64("file_id", plan.File.ID), slog.Any("public_keys", plan.Paths),
	)
	// No database transaction/row lock is held across storage IO. A partial
	// failure leaves the file hidden and the complete artifact set retryable.
	if err := u.deletePaths(ctx, plan.Paths); err != nil {
		u.publish(ctx, plan.UserID, u.buildFailedEvent(plan.File, err))
		return Response{}, err
	}
	changed := false
	err = u.transactor.RunInTx(ctx, func(ctx context.Context) error {
		current, found, err := planner.GetDeletionPlanForUpdate(ctx, req.FileID)
		if err != nil {
			return err
		}
		if !found {
			return errors.New("committed deletion plan disappeared")
		}
		if current.Completed {
			return nil
		}
		if err := u.enqueueAfterJobs(ctx, current.File.ID, current.File, current.AfterEvents); err != nil {
			return err
		}
		if err := planner.CompleteDeletionPlan(ctx, current.File.ID); err != nil {
			return err
		}
		changed = true
		return nil
	})
	if err != nil {
		return Response{}, err
	}
	if changed {
		u.publish(ctx, plan.UserID, u.buildCompletedEvent(plan.File))
	}
	return Response{}, nil
}

func (u *UseCase) prepareDeletion(ctx context.Context, req Request, planner deletionPlanner) (model.DeletionPlan, bool, error) {
	var plan model.DeletionPlan
	var found bool
	err := u.transactor.RunInTx(ctx, func(ctx context.Context) error {
		var err error
		plan, found, err = planner.GetDeletionPlanForUpdate(ctx, req.FileID)
		if err != nil {
			return err
		}
		if found {
			return validateOwnership(plan.File, req)
		}
		file, err := u.files.GetByIDForUpdate(ctx, req.FileID)
		if err != nil {
			return fmt.Errorf("lock deleted file: %w", err)
		}
		if file.ID == 0 {
			// Another deletion can have committed while this transaction waited
			// for the file lock. Read its plan again under READ COMMITTED.
			plan, found, err = planner.GetDeletionPlanForUpdate(ctx, req.FileID)
			if err != nil {
				return err
			}
			if found {
				return validateOwnership(plan.File, req)
			}
			return nil
		}
		if err := validateOwnership(file, req); err != nil {
			return err
		}
		plan = model.DeletionPlan{
			File:        file,
			Paths:       collectPaths(file, file.GetFullPath()),
			AfterEvents: append([]shared.FileEventAfterJob(nil), req.AfterEvents...),
			UserID:      req.UserID,
		}
		if err := planner.SaveDeletionPlan(ctx, plan); err != nil {
			return fmt.Errorf("save deletion plan: %w", err)
		}
		if err := u.files.DeleteByID(ctx, file.ID); err != nil {
			return fmt.Errorf("hide deleted file: %w", err)
		}
		found = true
		return nil
	})
	if err != nil {
		return model.DeletionPlan{}, false, err
	}
	return plan, found, nil
}

func (u *UseCase) deletePaths(ctx context.Context, paths []string) error {
	if len(paths) == 0 {
		return nil
	}
	if len(paths) > 1 {
		err := u.storage.DeleteBatch(ctx, paths)
		if err == nil {
			return nil
		}
		if !errors.Is(err, filestorage.ErrNotSupported) {
			return fmt.Errorf("storage delete batch: %w", err)
		}
	}
	for _, key := range paths {
		if err := u.storage.Delete(ctx, key); err != nil {
			return fmt.Errorf("storage delete file: %w", err)
		}
	}
	return nil
}

func (u *UseCase) publish(ctx context.Context, userID sharedtypes.UserID, event shared.FileDeletedEvent) {
	if err := event.Validate(); err != nil {
		u.logger.WarnContext(ctx, "invalid event", logger.Error(err))
		return
	}
	if userID.IsZero() {
		return
	}
	if err := u.eventStream.Publish(ctx, userID, event); err != nil {
		u.logger.WarnContext(ctx, "publish event", logger.Error(err))
	}
}

func (u *UseCase) enqueueAfterJobs(ctx context.Context, fileID int64, file model.File, events []shared.FileEventAfterJob) error {
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
		if value := u.publicURL(file); value != "" {
			meta["fileUrl"] = value
		}
		if file.FileName != "" {
			meta["fileName"] = file.FileName
		}
		if file.OriginalFileName != "" {
			meta["originalFileName"] = file.OriginalFileName
		}
		for key, value := range event.Meta {
			meta[key] = value
		}
		payload, err := eventfileafterprocess.MarshalPayload(
			eventfileafterprocess.NewPayload(event.UserID, shared.UserTypeAdmin, fileID, jobName, meta),
		)
		if err != nil {
			return fmt.Errorf("marshal after job payload: %w", err)
		}
		if _, err := u.outbox.Put(ctx, jobName, payload, time.Now()); err != nil {
			return fmt.Errorf("outbox put: %w", err)
		}
	}
	return nil
}

func collectPaths(file model.File, fallback string) []string {
	paths := make([]string, 0, len(file.GetData().Presets)+2)
	unique := make(map[string]struct{}, cap(paths))
	add := func(value string) {
		value = strings.TrimSpace(value)
		if value == "" {
			return
		}
		if _, ok := unique[value]; ok {
			return
		}
		unique[value] = struct{}{}
		paths = append(paths, value)
	}
	add(fallback)
	if file.ID == 0 {
		return paths
	}
	add(file.GetFullPath())
	for presetName, preset := range file.GetData().Presets {
		add(preset.RelativePath)
		name := strings.TrimSpace(preset.PresetName)
		if name == "" {
			name = presetName.String()
		}
		if name == "" || shared.PresetName(name) == shared.FilePresetMainName {
			continue
		}
		if safe, err := filesanitize.SanitizeSegment(name); err == nil && safe == name {
			add(path.Join(file.FolderPath, name, file.FileName))
		}
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
		return fmt.Errorf("%w: file_id=%d expected_object_type=%s expected_object_id=%d",
			ErrFileOwnershipMismatch, req.FileID, req.ObjectType, req.ObjectID,
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
