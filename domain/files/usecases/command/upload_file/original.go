package uploadfile

import (
	"context"
	"errors"
	"fmt"
	"io"
	"strings"

	logger "github.com/assurrussa/gologger"
	eventstream "github.com/assurrussa/gowebsocket/eventstream"

	"github.com/assurrussa/gouploads/domain/files/model"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
)

type OriginalRepository interface {
	fileRepository
	GetByIDForUpdate(ctx context.Context, fileID int64) (model.File, error)
}

type OriginalStorage interface {
	fileStorage
	Open(ctx context.Context, relativePath string) (io.ReadCloser, error)
}

// OriginalOptions deliberately has no resizer client, endpoint, token, or
// callback. All persistence dependencies must share one transaction context.
type OriginalOptions struct {
	Transaction     transactor
	Repository      OriginalRepository
	Storage         OriginalStorage
	Outbox          outboxPutter
	Events          eventstream.EventStream
	Logger          logger.Logger
	BaseFolder      string
	DeliveryBaseURL string
	StagingPrefixes []string
}

// OriginalUseCase reuses media finalization's artifact validation, storage,
// metadata, event and cleanup primitives. Its only input is a persisted file ID.
type OriginalUseCase struct {
	core     *UseCase
	repo     OriginalRepository
	prefixes []string
}

func NewOriginal(opts OriginalOptions) (*OriginalUseCase, error) {
	prefixes, err := normalizeOriginalPrefixes(opts.StagingPrefixes)
	if err != nil {
		return nil, err
	}
	setters := []OptOptionsSetter{WithDeliveryBaseURL(opts.DeliveryBaseURL)}
	if opts.BaseFolder != "" {
		setters = append(setters, WithBaseFolder(opts.BaseFolder))
	}
	core, err := New(NewOptions(
		opts.Transaction, opts.Repository, storedArtifactSource{storage: opts.Storage},
		opts.Events, opts.Logger, opts.Storage, opts.Outbox, setters...,
	))
	if err != nil {
		return nil, err
	}
	base := strings.Trim(core.baseFolder, "/")
	if base == "" || base != core.baseFolder {
		return nil, errors.New("original destination must be a canonical relative directory")
	}
	if _, err := normalizeOriginalPrefixes([]string{base}); err != nil {
		return nil, err
	}
	for _, prefix := range prefixes {
		if base == prefix || strings.HasPrefix(base, prefix+"/") || strings.HasPrefix(prefix, base+"/") {
			return nil, errors.New("original destination and staging prefixes must not overlap")
		}
	}
	return &OriginalUseCase{core: core, repo: opts.Repository, prefixes: prefixes}, nil
}

func (u *OriginalUseCase) HandleOriginal(ctx context.Context, fileID int64) error {
	if fileID <= 0 {
		return errors.New("original finalization requires a positive file ID")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	var completed model.File
	var uploader shared.FileUploader
	changed := false
	// Lock through copying and the metadata/outbox commit. This intentionally
	// trades transaction duration for cross-worker serialization without a new
	// lease/schema. Configure bounded worker concurrency and storage timeouts.
	err := u.core.transactor.RunInTx(ctx, func(txCtx context.Context) error {
		file, err := u.repo.GetByIDForUpdate(txCtx, fileID)
		if err != nil {
			return fmt.Errorf("lock original file: %w", err)
		}
		if file.ID == 0 || isCompletedFile(file, file.GetData().Uploader) {
			return nil // Deleted or already finalized: do not resurrect/re-enqueue.
		}
		uploader = file.GetData().Uploader
		if uploader.Status != shared.FileUploadTaskStatusQueued {
			return fmt.Errorf("original file is not queued: %s", uploader.Status)
		}
		source := file.GetFullPath()
		if err := validateOriginalKey(source, u.prefixes); err != nil {
			return err
		}
		completed, err = u.storeOriginal(txCtx, file)
		if err != nil {
			return err
		}
		if err := u.repo.Update(txCtx, fileID, completed); err != nil {
			return fmt.Errorf("save original metadata: %w", err)
		}
		if err := u.core.enqueueAfterJobs(txCtx, uploader, completed); err != nil {
			return fmt.Errorf("schedule original after jobs: %w", err)
		}
		if err := u.core.enqueueCleanup(txCtx, uploader.UserUUID, source); err != nil {
			return fmt.Errorf("schedule original staging cleanup: %w", err)
		}
		changed = true
		return nil
	})
	if err != nil {
		// Keep staging and deterministic final objects retryable. In particular,
		// never enqueue final-key deletion after an ambiguous commit outcome.
		return err
	}
	if changed {
		completed.URL = u.core.publicURL(completed.GetFullPath())
		u.core.publish(ctx, uploader.UserUUID, createEvent(uploader, completed, shared.FileUploadTaskStatusCompleted))
	}
	return nil
}

func (u *OriginalUseCase) storeOriginal(ctx context.Context, file model.File) (model.File, error) {
	if file.Size <= 0 || file.Size > maxArtifactSize {
		return model.File{}, errors.New("original size is outside supported limits")
	}
	artifact := Artifact{
		Preset: shared.FilePresetMainName.String(), URL: file.GetFullPath(),
		ContentType: file.MimeType, Size: file.Size,
	}
	// Local source artifacts do not have a callback URL or an expiry. The same
	// content validation and checksum/save path is used as for processed media.
	stored, err := u.core.saveFileStorage(ctx, artifact, file)
	if err != nil {
		return model.File{}, fmt.Errorf("store original: %w", err)
	}
	data := file.GetData()
	if err := u.core.updateMainArtifact(&file, data, stored, nil); err != nil {
		return model.File{}, err
	}
	data.Presets = map[shared.PresetName]shared.FilePreset{
		shared.FilePresetMainName: {
			PresetName: artifact.Preset, RelativePath: stored.RelativePath,
			Size: stored.Size, MimeType: stored.MimeType, ChecksumSHA256: stored.Checksum,
			Width: data.Width, Height: data.Height,
		},
	}
	data.Uploader = shared.FileUploader{}
	file.SetData(data)
	return file, nil
}

// storedArtifactSource implements the existing artifact-read port with object
// storage instead of HTTP. It never interprets the source as a network URL.
type storedArtifactSource struct {
	storage OriginalStorage
}

func (s storedArtifactSource) DownloadFile(ctx context.Context, req clientresizer.RequestDownload) (clientresizer.ResponseDownload, error) {
	body, err := s.storage.Open(ctx, req.URL)
	if err != nil {
		return clientresizer.ResponseDownload{}, err
	}
	if body == nil {
		return clientresizer.ResponseDownload{}, errors.New("original storage returned a nil reader")
	}
	return clientresizer.ResponseDownload{Body: body}, nil
}
