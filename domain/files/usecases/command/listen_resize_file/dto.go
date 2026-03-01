package listenresizefile

import (
	"time"

	"github.com/assurrussa/goshared/pkg/validator"
)

type Request struct {
	ExternalID int64
	Status     string `validate:"required"`
	Error      string
	Attempt    uint32
	Metadata   map[string]any
	Timestamp  time.Time
	Artifacts  []Artifact `validate:"required,dive"`
}

type Artifact struct {
	Preset      string         `json:"preset" validate:"required"`
	URL         string         `json:"url" validate:"required"`
	MediaType   string         `json:"mediaType,omitempty"`
	ContentType string         `json:"contentType,omitempty"`
	Size        int64          `json:"size,omitempty"`
	ExpireAt    time.Time      `json:"expireAt,omitempty"`
	Metadata    map[string]any `json:"metadata,omitempty"`
}

func (r Request) Validate() error {
	return validator.Validator.Struct(r)
}

type Response struct{}
