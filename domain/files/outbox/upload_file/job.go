package uploadfilejob

import (
	"context"
	"fmt"

	"github.com/assurrussa/goshared/pkg/logger"
	sharedjob "github.com/assurrussa/outbox/shared/job"

	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
)

//go:generate toolsmocks

const (
	JobName    = "upload_file_persist"
	loggerName = "job_upload_file_persist"
)

type uploadFileUseCase interface {
	Handle(ctx context.Context, req uploadfile.Request) (uploadfile.Response, error)
}

//go:generate options-gen -out-filename=job_options.gen.go -from-struct=Options
type Options struct {
	uploadFileUseCase uploadFileUseCase `option:"mandatory" validate:"required"`
	logger            logger.Logger     `option:"mandatory" validate:"required"`
}

type Job struct {
	sharedjob.DefaultJob
	Options
}

func Must(opts Options) *Job {
	j, err := New(opts)
	if err != nil {
		panic(err)
	}

	return j
}

func New(opts Options) (*Job, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate job options: %w", err)
	}

	opts.logger = opts.logger.WithNamed(loggerName)

	return &Job{
		Options: opts,
	}, nil
}

func (j *Job) Name() string { return JobName }

func (j *Job) Handle(ctx context.Context, payload string) error {
	data, err := UnmarshalPayload(payload)
	if err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	artifacts := make([]uploadfile.Artifact, 0, len(data.Artifacts))
	for _, artifact := range data.Artifacts {
		artifacts = append(artifacts, uploadfile.Artifact{
			Preset:      artifact.Preset,
			URL:         artifact.URL,
			MediaType:   artifact.MediaType,
			ContentType: artifact.ContentType,
			Size:        artifact.Size,
			ExpireAt:    artifact.ExpireAt,
			Metadata:    artifact.Metadata,
		})
	}

	_, err = j.uploadFileUseCase.Handle(ctx, uploadfile.Request{
		FileID:    data.FileID,
		Artifacts: artifacts,
	})
	if err != nil {
		return fmt.Errorf("get task %d: %w", data.FileID, err)
	}

	return nil
}
