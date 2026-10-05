package clientresizer

import (
	"io"

	outboxtypes "github.com/assurrussa/outbox/shared/types"

	validator "github.com/assurrussa/gouploads/internal/validation"
)

type Request struct {
	TypeMedia string `validate:"required,oneof=image video"`
	Data      []byte `validate:"required"`
}

func (r Request) Validate() error {
	return validator.Validator.Struct(r)
}

type Response struct {
	JobID  outboxtypes.JobID `json:"job_id"` //nolint:tagliatelle // external client
	Status string            `json:"status"`
}

type RequestDownload struct {
	Preset string `validate:"required"`
	URL    string `validate:"required"`
	// TypeMedia selects the resizer that owns the processing job, not the
	// artifact's MIME type. A video's image preview still belongs to video.
	TypeMedia string `validate:"required"`
}

func (r RequestDownload) Validate() error {
	return validator.Validator.Struct(r)
}

type ResponseDownload struct {
	Body          io.ReadCloser
	ContentType   string
	ContentLength int64
}
