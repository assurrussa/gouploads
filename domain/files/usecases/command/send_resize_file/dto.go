package sendresizefile

import (
	"errors"
	"strings"
	"time"

	outboxtypes "github.com/assurrussa/outbox/shared/types"

	validator "github.com/assurrussa/gouploads/internal/validation"
)

type Request struct {
	FileID          int64 `validate:"gt=0"`
	FilePath        string
	JobID           *outboxtypes.JobID
	PollDeadline    *time.Time
	SkipResizeVideo bool
}

func (r Request) Validate() error {
	if err := validator.Validator.Struct(r); err != nil {
		return err
	}
	if (r.JobID == nil) != (r.PollDeadline == nil) {
		return errors.New("media continuation requires both job ID and deadline")
	}
	if r.JobID == nil && strings.TrimSpace(r.FilePath) == "" {
		return errors.New("media submission requires a source path")
	}
	return nil
}

type Response struct {
	JobID  outboxtypes.JobID
	Status string
}
