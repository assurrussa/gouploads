package clientresizer

import (
	"github.com/assurrussa/goshared/pkg/validator"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
)

type Request struct {
	TypeMedia string `validate:"required"`
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
	Preset    string `validate:"required"`
	URL       string `validate:"required"`
	TypeMedia string `validate:"required"`
}

func (r RequestDownload) Validate() error {
	return validator.Validator.Struct(r)
}

type ResponseDownload struct {
	Body []byte `json:"body"`
}
