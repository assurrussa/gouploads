package listenresizefilejob

import (
	"context"
	"fmt"

	logger "github.com/assurrussa/gologger"
	sharedjob "github.com/assurrussa/outbox/shared/job"

	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
)

//go:generate toolsmocks

const (
	JobName    = "listen_resize_file"
	loggerName = "job_listen_resize_file"
)

type listenResizeFileUseCase interface {
	Handle(ctx context.Context, req listenresizefile.Request) (listenresizefile.Response, error)
}

//go:generate options-gen -out-filename=job_options.gen.go -from-struct=Options
type Options struct {
	listenResizeFileUseCase listenResizeFileUseCase `option:"mandatory" validate:"required"`
	logger                  logger.Logger           `option:"mandatory" validate:"required"`
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

	_, err = j.listenResizeFileUseCase.Handle(ctx, listenresizefile.Request{
		ExternalID: data.ExternalID,
		Status:     data.State,
		Error:      data.Error,
		Metadata:   data.Metadata,
		Artifacts:  data.Artifacts,
	})
	if err != nil {
		return fmt.Errorf("listen resile file %d: %w", data.ExternalID, err)
	}

	return nil
}
