package uploadfilejob

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

type Payload struct {
	FileID    int64      `json:"fileId"`
	Artifacts []Artifact `json:"artifacts"`
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

func NewPayload(fileID int64, artifacts []Artifact) Payload {
	return Payload{FileID: fileID, Artifacts: artifacts}
}

func MarshalPayload(p Payload) (string, error) {
	if p.FileID == 0 || len(p.Artifacts) == 0 {
		return "", errors.New("invalid payload: fileId or artifacts is zero")
	}

	b, err := json.Marshal(p)
	if err != nil {
		return "", fmt.Errorf("marshal payload: %w", err)
	}

	return string(b), nil
}

func UnmarshalPayload(data string) (Payload, error) {
	var p Payload
	if err := json.Unmarshal([]byte(data), &p); err != nil {
		return Payload{}, fmt.Errorf("unmarshal payload: %w", err)
	}

	if p.FileID == 0 || len(p.Artifacts) == 0 {
		return Payload{}, errors.New("invalid payload: fileId or artifacts is zero")
	}

	return p, nil
}
