package uploadfile

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	logger "github.com/assurrussa/gologger"
	sharedjob "github.com/assurrussa/outbox/shared/job"
	outboxtypes "github.com/assurrussa/outbox/shared/types"

	"github.com/assurrussa/gouploads/domain/files/model"
	deletedfilejob "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	eventfileafterprocess "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process"
	"github.com/assurrussa/gouploads/domain/files/shared"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	eventstream "github.com/assurrussa/gouploads/internal/events"
	"github.com/assurrussa/gouploads/internal/filepolicy"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

const (
	contentTypeImageWebP = "image/webp"
	extensionWebP        = ".webp"
	maxArtifactSize      = filepolicy.MaxFileSize
	artifactSniffSize    = 512
	failedCleanupTimeout = 30 * time.Second
)

//go:generate toolsmocks

type fileRepository interface {
	GetByID(ctx context.Context, fileID int64) (model.File, error)
	Update(ctx context.Context, fileID int64, file model.File) error
}
type outboxPutter interface {
	Put(ctx context.Context, name, payload string, availableAt time.Time) (outboxtypes.JobID, error)
}
type fileStorage interface {
	SavePersist(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error)
	Delete(ctx context.Context, relativePath string) error
}
type resizeClient interface {
	DownloadFile(ctx context.Context, req clientresizer.RequestDownload) (clientresizer.ResponseDownload, error)
}
type transactor interface {
	RunInTx(ctx context.Context, f func(context.Context) error) error
}

type fileStorageDTO struct {
	RelativePath string
	FolderPath   string
	FileName     string
	URL          string
	Size         int64
	MimeType     string
	Width        int
	Height       int
	Checksum     string
}

//go:generate options-gen -out-filename=usecase_options.gen.go -from-struct=Options
type Options struct {
	transactor      transactor            `option:"mandatory" validate:"required"`
	fileRepository  fileRepository        `option:"mandatory" validate:"required"`
	resizeClient    resizeClient          `option:"mandatory" validate:"required"`
	eventStream     eventstream.Publisher `option:"mandatory" validate:"required"`
	logger          logger.Logger         `option:"mandatory" validate:"required"`
	storage         fileStorage           `option:"mandatory" validate:"required"`
	outbox          outboxPutter          `option:"mandatory" validate:"required"`
	baseFolder      string                `default:"media/v1"`
	deliveryBaseURL string                `validate:"omitempty,url"`
}

type UseCase struct {
	sharedjob.DefaultJob
	Options
	scanner            filepolicy.Scanner
	allowAudioOriginal bool
}

func Must(opts Options) *UseCase {
	useCase, err := New(opts)
	if err != nil {
		panic(err)
	}
	return useCase
}

func New(opts Options) (*UseCase, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate job options: %w", err)
	}
	return &UseCase{Options: opts}, nil
}

// NewWithContentScanner adds optional complete-source scanning before publishing
// any bytes. It does not change the generated Options or legacy constructor.
func NewWithContentScanner(opts Options, scanner filepolicy.Scanner) (*UseCase, error) {
	useCase, err := New(opts)
	if err != nil {
		return nil, err
	}
	useCase.scanner = scanner
	return useCase, nil
}

func (u *UseCase) Handle(ctx context.Context, req Request) (Response, error) {
	if err := req.Validate(); err != nil {
		return Response{}, fmt.Errorf("validate request: %w", err)
	}
	repo, ok := u.fileRepository.(lockingRepository)
	if !ok {
		return Response{}, errors.New("media finalization requires a locking file repository")
	}
	var completed model.File
	var uploader shared.FileUploader
	changed := false
	err := u.transactor.RunInTx(ctx, func(ctx context.Context) error {
		file, err := repo.GetByIDForUpdate(ctx, req.FileID)
		if err != nil {
			return fmt.Errorf("lock file %d: %w", req.FileID, err)
		}
		if file.ID == 0 || isCompletedFile(file, file.GetData().Uploader) {
			return nil
		}
		uploader = file.GetData().Uploader
		source := file.GetFullPath()
		completed, _, err = u.processFileModel(ctx, req, file)
		if err != nil {
			return fmt.Errorf("process file model: %w", err)
		}
		completed.UpdatedAt = time.Now()
		if err := u.fileRepository.Update(ctx, file.ID, completed); err != nil {
			return fmt.Errorf("save file: %w", err)
		}
		if err := u.enqueueAfterJobs(ctx, uploader, completed); err != nil {
			return fmt.Errorf("schedule after jobs: %w", err)
		}
		if source != "" {
			if err := u.enqueueCleanup(ctx, uploader.UserUUID, source); err != nil {
				return fmt.Errorf("schedule staging cleanup: %w", err)
			}
		}
		changed = true
		return nil
	})
	if err != nil {
		// Never destroy deterministic final keys on an uncertain commit, or
		// while another retry may be using them. CleanupOnFailure is retained
		// in persisted payloads for decoding compatibility, not destructive IO.
		return Response{}, err
	}
	if changed {
		completed.URL = u.publicURL(completed.GetFullPath())
		u.publish(ctx, uploader.UserUUID, createEvent(uploader, completed, shared.FileUploadTaskStatusCompleted))
	}
	return Response{}, nil
}

func (u *UseCase) enqueueAfterJobs(ctx context.Context, uploader shared.FileUploader, file model.File) error {
	for _, job := range uploader.AfterJobs {
		name := strings.TrimSpace(job.JobName)
		if name == "" {
			continue
		}
		payload, err := u.createPayload(file, job, uploader)
		if err != nil {
			return fmt.Errorf("outbox create payload: %w", err)
		}
		if _, err := u.outbox.Put(ctx, name, payload, time.Now()); err != nil {
			return fmt.Errorf("outbox put: %w", err)
		}
	}
	return nil
}

func (u *UseCase) enqueueCleanup(ctx context.Context, userID sharedtypes.UserID, key string) error {
	payload, err := deletedfilejob.MarshalPayload(deletedfilejob.NewPayload(0, userID, key))
	if err != nil {
		return fmt.Errorf("marshal cleanup payload: %w", err)
	}
	if _, err := u.outbox.Put(ctx, deletedfilejob.JobName, payload, time.Now()); err != nil {
		return fmt.Errorf("put cleanup job: %w", err)
	}
	return nil
}

func isCompletedFile(file model.File, uploader shared.FileUploader) bool {
	return uploader.Status == shared.FileUploadTaskStatusCompleted || (uploader.Status == "" && len(file.GetData().Presets) > 0)
}

func (u *UseCase) createPayload(file model.File, job shared.FileEventAfterJob, uploader shared.FileUploader) (string, error) {
	if job.Payload != "" {
		return job.Payload, nil
	}
	meta := map[string]any{
		"fileId":           file.ID,
		"fileUrl":          u.publicURL(file.GetFullPath()),
		"fileName":         file.FileName,
		"originalFileName": file.OriginalFileName,
		"objectType":       file.ObjectType.String(),
		"objectId":         file.ObjectID.String(),
	}
	for key, value := range job.Meta {
		meta[key] = value
	}
	return eventfileafterprocess.MarshalPayload(
		eventfileafterprocess.NewPayload(uploader.UserUUID, uploader.Type, file.ID, job.JobName, meta))
}

func (u *UseCase) publish(ctx context.Context, userID sharedtypes.UserID, event shared.FileUploadStatusEvent) {
	if err := u.eventStream.Publish(ctx, userID, event); err != nil {
		u.logger.WarnContext(ctx, "publish event", logger.Error(err))
	}
}

func createEvent(uploader shared.FileUploader, file model.File, status shared.FileUploadTaskStatus) shared.FileUploadStatusEvent {
	event := shared.NewFileUploadStatusEvent(file.ID, status)
	event.Metadata = map[string]any{
		"name":         "upload_file",
		"uploaderUuid": uploader.UserUUID.String(),
		"objectType":   file.ObjectType.String(),
		"objectId":     file.ObjectID.String(),
	}
	event.File = buildEventFileEnvelope(file)
	return event
}

func buildEventFileEnvelope(file model.File) *shared.FileUploadEventFile {
	return &shared.FileUploadEventFile{
		ID:           file.ID,
		FileName:     file.FileName,
		OriginalName: file.OriginalFileName,
		URL:          file.GetPublicURL(),
		Size:         file.Size,
		MimeType:     file.MimeType,
		Width:        file.GetWidth(),
		Height:       file.GetHeight(),
		IsPrimary:    file.IsPrimary,
	}
}
