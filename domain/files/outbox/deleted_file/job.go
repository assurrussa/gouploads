package deletedfilejob

import (
	"context"
	"fmt"

	logger "github.com/assurrussa/gologger"
	sharedjob "github.com/assurrussa/outbox/shared/job"

	deletedfile "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
)

//go:generate toolsmocks

const (
	JobName    = "deleted_file"
	loggerName = "job_deleted_file"
)

type deleteFileUseCase interface {
	Handle(ctx context.Context, req deletedfile.Request) (deletedfile.Response, error)
}

//go:generate options-gen -out-filename=job_options.gen.go -from-struct=Options
type Options struct {
	deleteFileUseCase deleteFileUseCase `option:"mandatory" validate:"required"`
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

func (j *Job) Handle(ctx context.Context, payload string) (errReturn error) {
	data, err := UnmarshalPayload(payload)
	if err != nil {
		return fmt.Errorf("unmarshal payload: %w", err)
	}

	_, err = j.deleteFileUseCase.Handle(ctx, deletedfile.Request{
		UserID:      data.UserID,
		FileID:      data.FileID,
		FilePath:    data.FilePath,
		ObjectType:  data.ObjectType,
		ObjectID:    data.ObjectID,
		AfterEvents: data.AfterEvents,
	})
	if err != nil {
		return fmt.Errorf("deleteFileUseCase %d: %w", data.FileID, err)
	}

	return nil
}
