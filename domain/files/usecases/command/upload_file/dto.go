package uploadfile

import (
	"time"

	validator "github.com/assurrussa/gouploads/internal/validation"
)

type Request struct {
	FileID           int64      `validate:"required"`
	Artifacts        []Artifact `validate:"required"`
	CleanupOnFailure bool
}

type Artifact struct {
	Preset      string `validate:"required"`
	URL         string `validate:"required"`
	MediaType   string
	ContentType string
	Size        int64
	ExpireAt    time.Time
	Metadata    map[string]any
}

func (r Request) Validate() error {
	return validator.Validator.Struct(r)
}

type Response struct{}
