package sendresizefile

import (
	"github.com/assurrussa/goshared/pkg/validator"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
)

type Request struct {
	FileID          int64  `validate:"required"`
	FilePath        string `validate:"required"`
	SkipResizeVideo bool
}

func (r Request) Validate() error {
	return validator.Validator.Struct(r)
}

type Response struct {
	JobID  outboxtypes.JobID
	Status string
}
