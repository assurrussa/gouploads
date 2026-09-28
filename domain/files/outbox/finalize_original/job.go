package finalizeoriginal

import (
	"context"
	"errors"
	"fmt"
	"reflect"

	sharedjob "github.com/assurrussa/outbox/shared/job"
)

type Handler interface {
	HandleOriginal(ctx context.Context, fileID int64) error
}

// Job finalizes stored originals through the host's durable outbox worker.
type Job struct {
	sharedjob.DefaultJob
	handler Handler
}

func New(handler Handler) (*Job, error) {
	if handler == nil || reflect.ValueOf(handler).Kind() == reflect.Pointer && reflect.ValueOf(handler).IsNil() {
		return nil, errors.New("original finalization handler is required")
	}
	return &Job{handler: handler}, nil
}

func (j *Job) Name() string { return JobName }

func (j *Job) Handle(ctx context.Context, data string) error {
	payload, err := UnmarshalPayload(data)
	if err != nil {
		return err
	}
	if err := j.handler.HandleOriginal(ctx, payload.FileID); err != nil {
		return fmt.Errorf("finalize original file %d: %w", payload.FileID, err)
	}
	return nil
}
