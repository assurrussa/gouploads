package listenresizefile

import (
	"errors"
	"time"

	validator "github.com/assurrussa/gouploads/internal/validation"
)

type Request struct {
	ExternalID int64  `validate:"gt=0"`
	Status     string `validate:"required"`
	Error      string
	Attempt    uint32
	Metadata   map[string]any
	Timestamp  time.Time
	Artifacts  []Artifact `validate:"dive"`
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
	if err := validator.Validator.Struct(r); err != nil {
		return err
	}
	if r.Status == "done" && len(r.Artifacts) == 0 {
		return errors.New("completed media job requires artifacts")
	}
	return nil
}

type Response struct{}
