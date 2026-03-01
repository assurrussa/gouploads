package sendresizefilejob

import (
	"context"
	"fmt"

	"github.com/assurrussa/goshared/pkg/logger"
	sharedjob "github.com/assurrussa/outbox/shared/job"

	sendresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
)

//go:generate toolsmocks

const (
	JobName    = "send_resize_file"
	loggerName = "job_send_resize_file"
)

type sendResizeFileUseCase interface {
	Handle(ctx context.Context, req sendresizefile.Request) (sendresizefile.Response, error)
}

//go:generate options-gen -out-filename=job_options.gen.go -from-struct=Options
type Options struct {
	sendResizeFileUseCase sendResizeFileUseCase `option:"mandatory" validate:"required"`
	logger                logger.Logger         `option:"mandatory" validate:"required"`
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

func (j *Job) Handle(ctx context.Context, payload string) (errReturn error) {
	data, err := UnmarshalPayload(payload)
	if err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	_, err = j.sendResizeFileUseCase.Handle(ctx, sendresizefile.Request{
		FilePath:        data.FilePath,
		FileID:          data.FileID,
		SkipResizeVideo: data.SkipResizeVideo,
	})
	if err != nil {
		return fmt.Errorf("send resile file %d: %w", data.FileID, err)
	}

	return nil
}
