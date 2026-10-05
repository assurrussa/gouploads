package sendresizefile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strconv"
	"time"

	logger "github.com/assurrussa/gologger"
	sharedjob "github.com/assurrussa/outbox/shared/job"
	outboxtypes "github.com/assurrussa/outbox/shared/types"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
	eventstream "github.com/assurrussa/gouploads/internal/events"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

//go:generate toolsmocks

type fileRepository interface {
	GetByID(ctx context.Context, taskID int64) (model.File, error)
}

type resizeClient interface {
	SendResize(ctx context.Context, req clientresizer.Request) (clientresizer.Response, error)
	GetJob(ctx context.Context, req clientresizer.JobRequest) (clientresizer.JobResponse, error)
}

type outboxPutter interface {
	Put(ctx context.Context, name, payload string, availableAt time.Time) (outboxtypes.JobID, error)
}
type resultHandler interface {
	Handle(ctx context.Context, req listenresizefile.Request) (listenresizefile.Response, error)
}

type sourceURLResolver interface {
	Resolve(ctx context.Context, source string) (string, error)
}

//go:generate options-gen -out-filename=usecase_options.gen.go -from-struct=Options
type Options struct {
	fileRepository fileRepository             `option:"mandatory" validate:"required"`
	remoteClient   resizeClient               `option:"mandatory" validate:"required"`
	sourceResolver sourceURLResolver          `option:"mandatory" validate:"required"`
	eventStream    eventstream.Publisher      `option:"mandatory" validate:"required"`
	imagePipeline  config.ImagePipelineConfig `option:"mandatory" validate:"required"`
	videoPipeline  config.VideoPipelineConfig `option:"mandatory" validate:"required"`
	logger         logger.Logger              `option:"mandatory" validate:"required"`
	outbox         outboxPutter               `option:"mandatory" validate:"required"`
	resultHandler  resultHandler              `option:"mandatory" validate:"required"`
}

type UseCase struct {
	sharedjob.DefaultJob
	Options
}

func Must(opts Options) *UseCase {
	j, err := New(opts)
	if err != nil {
		panic(err)
	}

	return j
}

func New(opts Options) (*UseCase, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate job options: %w", err)
	}

	return &UseCase{
		Options: opts,
	}, nil
}

func (u *UseCase) Handle(ctx context.Context, req Request) (resp Response, errReturn error) {
	if err := req.Validate(); err != nil {
		return Response{}, fmt.Errorf("validate request: %w", err)
	}

	fileModel, err := u.fileRepository.GetByID(ctx, req.FileID)
	if err != nil {
		return Response{}, fmt.Errorf("get fileModel %d: %w", req.FileID, err)
	}

	taskLogger := u.logger.WithAttrs(
		slog.Int64("file_id", fileModel.ID),
		slog.String("status", fileModel.GetData().Uploader.Status.String()),
	)

	if fileModel.ID == 0 || fileModel.IsUploadCompleted() {
		taskLogger.DebugContext(ctx, "fileModel already completed")
		return Response{}, nil
	}

	if req.JobID != nil {
		return u.poll(ctx, fileModel, req)
	}

	// Dispatch uncertainty is not a terminal media failure. Return the error
	// to the durable worker; retry the same key without publishing Failed.

	sourceURL, err := u.sourceResolver.Resolve(ctx, req.FilePath)
	if err != nil {
		return Response{}, fmt.Errorf("resolve source URL: %w", err)
	}
	req.FilePath = sourceURL

	payload, err := u.createPayload(fileModel, req)
	if err != nil {
		return Response{}, fmt.Errorf("create payload: %w", err)
	}

	body, err := MarshalPayload(payload)
	if err != nil {
		return Response{}, fmt.Errorf("json marshal payload: %w", err)
	}

	respClient, err := u.remoteClient.SendResize(ctx, clientresizer.Request{
		TypeMedia: fileModel.FileType.ToString(),
		Data:      body,
	})
	if err != nil {
		return Response{}, fmt.Errorf("resize request: %w", err)
	}

	jobID := respClient.JobID
	jobStatus := respClient.Status
	deadline := time.Now().Add(7 * 24 * time.Hour)
	req.JobID = &jobID
	req.PollDeadline = &deadline
	// Drop the signed source URL. Polling never resolves or submits it again.
	// An old worker also fails source-path validation rather than re-submitting.
	req.FilePath = ""
	if err := u.schedulePoll(ctx, req); err != nil {
		return Response{}, err
	}
	// A retained failed admission also replies 202/queued. Replaying it cannot
	// restart this upload or move its notification back to Processing; the
	// idempotent failed poll would not publish a second corrective Failed event.
	if fileModel.GetData().Uploader.Status != shared.FileUploadTaskStatusFailed {
		u.publish(ctx, fileModel.GetData().Uploader.UserUUID, createEvent(
			fileModel, jobID, jobStatus, shared.FileUploadTaskStatusProcessing,
		))
	}

	return Response{
		JobID:  respClient.JobID,
		Status: respClient.Status,
	}, nil
}

func (u *UseCase) publish(ctx context.Context, userID sharedtypes.UserID, event shared.FileUploadStatusEvent) {
	if err := u.eventStream.Publish(ctx, userID, event); err != nil {
		u.logger.WarnContext(ctx, "publish event", logger.Error(err))
	}
}

func (u *UseCase) createPayload(fileModel model.File, req Request) (Payload, error) {
	fileIDKey := strconv.FormatUint(uint64(fileModel.ID), 10)
	meta := map[string]string{
		"fileId": fileIDKey,
	}

	var reqType string
	var webhookURL string
	var skipResize bool
	presets := make([]Preset, 0, len(u.imagePipeline.Presets))
	switch {
	case isImage(fileModel.MimeType):
		reqType = "image"
		webhookURL = u.imagePipeline.WebhookCallbackHost
		for _, preset := range u.imagePipeline.Presets {
			presets = append(presets, Preset{
				Name:    preset.Name,
				Format:  preset.Format,
				Height:  preset.Height,
				Width:   preset.Width,
				Quality: preset.Quality,
				Fit:     preset.Fit,
				Target:  "image",
			})
		}
	case isVideo(fileModel.MimeType):
		reqType = "video"
		skipResize = req.SkipResizeVideo
		webhookURL = u.videoPipeline.WebhookCallbackHost
		for _, preset := range u.videoPipeline.Presets {
			thumbnail := presetPreviewFromConfig(preset.Thumbnail)
			preview := presetPreviewFromConfig(preset.Preview)
			presets = append(presets, Preset{
				Name:         preset.Name,
				Format:       preset.Format,
				Height:       preset.Height,
				Width:        preset.Width,
				Quality:      preset.Quality,
				VideoBitrate: preset.VideoBitrate,
				AudioBitrate: preset.AudioBitrate,
				Target:       "video",
				Thumbnail:    thumbnail,
				Preview:      preview,
			})
		}
	default:
		return Payload{}, errors.New("unknown type resize")
	}

	return NewPayload(fileIDKey, reqType, skipResize, webhookURL, presets, Source{
		URL: req.FilePath,
	}, meta), nil
}

func presetPreviewFromConfig(cfg config.PresetPreviewConfig) *PresetPreview {
	if !cfg.Enabled &&
		cfg.Timestamp == 0 &&
		cfg.Width == 0 &&
		cfg.Height == 0 &&
		cfg.Format == "" {
		return nil
	}

	return &PresetPreview{
		Enabled:   cfg.Enabled,
		Timestamp: cfg.Timestamp,
		Width:     cfg.Width,
		Height:    cfg.Height,
		Format:    cfg.Format,
	}
}

func createEvent(
	task model.File,
	jobID outboxtypes.JobID,
	jobStatus string,
	status shared.FileUploadTaskStatus,
) shared.FileUploadStatusEvent {
	eventFileSendResizer := shared.NewFileUploadStatusEvent(task.ID, status)
	eventFileSendResizer.Metadata = map[string]any{
		"jobId":        jobID,
		"jobStatus":    jobStatus,
		"uploaderUuid": task.GetData().Uploader.UserUUID.String(),
		"objectType":   task.ObjectType.String(),
		"objectId":     task.ObjectID.String(),
	}
	eventFileSendResizer.File = buildEventFileEnvelope(task)

	return eventFileSendResizer
}

func isImage(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "image/"
}

func isVideo(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "video/"
}

func buildEventFileEnvelope(fileModel model.File) *shared.FileUploadEventFile {
	return &shared.FileUploadEventFile{
		ID:           fileModel.ID,
		FileName:     fileModel.FileName,
		OriginalName: fileModel.OriginalFileName,
		URL:          "",
		Size:         fileModel.Size,
		MimeType:     fileModel.MimeType,
		Width:        fileModel.GetWidth(),
		Height:       fileModel.GetHeight(),
		IsPrimary:    fileModel.IsPrimary,
	}
}
