package listenresizefile

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"time"

	logger "github.com/assurrussa/gologger"
	sharedjob "github.com/assurrussa/outbox/shared/job"
	outboxtypes "github.com/assurrussa/outbox/shared/types"

	"github.com/assurrussa/gouploads/domain/files/model"
	uploadfilejob "github.com/assurrussa/gouploads/domain/files/outbox/upload_file"
	"github.com/assurrussa/gouploads/domain/files/shared"
	eventstream "github.com/assurrussa/gouploads/internal/events"
	"github.com/assurrussa/gouploads/internal/filejobs"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

//go:generate toolsmocks

type fileRepository interface {
	GetByID(ctx context.Context, taskID int64) (model.File, error)
}

type outboxPutter interface {
	Put(ctx context.Context, name, payload string, availableAt time.Time) (outboxtypes.JobID, error)
}

//go:generate options-gen -out-filename=usecase_options.gen.go -from-struct=Options
type Options struct {
	fileRepository fileRepository        `option:"mandatory" validate:"required"`
	outboxPutter   outboxPutter          `option:"mandatory" validate:"required"`
	eventStream    eventstream.Publisher `option:"mandatory" validate:"required"`
	logger         logger.Logger         `option:"mandatory" validate:"required"`
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

	if req.Status == "failed" {
		return u.handleFailure(ctx, req)
	}

	if req.Status != "done" {
		u.logger.InfoContext(ctx, "listen resize file another state", slog.String("status", req.Status))

		return Response{}, nil
	}

	fileID := req.ExternalID
	fileModel, err := u.fileRepository.GetByID(ctx, fileID)
	if err != nil {
		return Response{}, fmt.Errorf("get fileModel %d: %w", fileID, err)
	}

	if fileModel.ID == 0 || fileModel.IsUploadCompleted() {
		return Response{}, nil
	}

	tmNow := time.Now()
	artifacts := make([]uploadfilejob.Artifact, 0, len(req.Artifacts))
	for _, artifact := range req.Artifacts {
		artifacts = append(artifacts, uploadfilejob.Artifact{
			Preset:      artifact.Preset,
			URL:         artifact.URL,
			MediaType:   artifact.MediaType,
			ContentType: artifact.ContentType,
			Size:        artifact.Size,
			ExpireAt:    artifact.ExpireAt,
			Metadata:    artifact.Metadata,
		})
	}
	payload, err := uploadfilejob.MarshalPayload(uploadfilejob.NewPayload(fileID, artifacts))
	if err != nil {
		return Response{}, fmt.Errorf("marshal payload: %w", err)
	}

	if _, err := filejobs.Put(ctx, u.outboxPutter, fileModel, model.FileJobMediaFinalization, uploadfilejob.JobName, payload, tmNow); err != nil {
		return Response{}, fmt.Errorf("admin Outbox: %w", err)
	}

	u.publish(ctx, fileModel.GetData().Uploader.UserUUID, createEvent(
		fileModel, req, shared.FileUploadTaskStatusProcessing,
	))

	return Response{}, nil
}

func (u *UseCase) publish(ctx context.Context, userID sharedtypes.UserID, event shared.FileUploadStatusEvent) {
	if err := u.eventStream.Publish(ctx, userID, event); err != nil {
		u.logger.WarnContext(ctx, "publish event", logger.Error(err))
	}
}

func createEvent(
	fileModel model.File,
	req Request,
	status shared.FileUploadTaskStatus,
) shared.FileUploadStatusEvent {
	presets := make([]string, 0, len(req.Artifacts))
	for _, artifact := range req.Artifacts {
		presets = append(presets, artifact.Preset)
	}

	eventFileSendResizer := shared.NewFileUploadStatusEvent(fileModel.ID, status)
	eventFileSendResizer.Metadata = map[string]any{
		"jobState":     req.Status,
		"presets":      presets,
		"uploaderUuid": fileModel.GetData().Uploader.UserUUID.String(),
		"objectType":   fileModel.ObjectType.String(),
		"objectId":     fileModel.ObjectID.String(),
	}
	eventFileSendResizer.File = buildEventFileEnvelope(fileModel)

	return eventFileSendResizer
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

// failureRepository is an optional capability for older custom repositories.
// Failed callbacks fail closed unless durable, completion-fenced updates exist.
type failureRepository interface {
	MarkMediaFailed(ctx context.Context, fileID int64) (model.File, bool, error)
}

func (u *UseCase) handleFailure(ctx context.Context, req Request) (Response, error) {
	repo, ok := u.fileRepository.(failureRepository)
	if !ok {
		return Response{}, errors.New("failed media callback requires a failure-capable file repository")
	}
	file, changed, err := repo.MarkMediaFailed(ctx, req.ExternalID)
	if err != nil {
		return Response{}, fmt.Errorf("persist media failure: %w", err)
	}
	if changed {
		u.publish(ctx, file.GetData().Uploader.UserUUID, createEvent(file, req, shared.FileUploadTaskStatusFailed))
	}
	return Response{}, nil
}
