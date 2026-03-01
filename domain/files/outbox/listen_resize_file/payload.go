package listenresizefilejob

import (
	"encoding/json"
	"errors"
	"fmt"

	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
)

type Payload struct {
	ExternalID int64                       `json:"externalId"`
	State      string                      `json:"state"`
	Error      string                      `json:"error"`
	Metadata   map[string]any              `json:"meta"`
	Artifacts  []listenresizefile.Artifact `json:"artifacts"`
}

func NewPayload(
	externalID int64,
	state string,
	errState string,
	metadata map[string]any,
	artifacts []listenresizefile.Artifact,
) Payload {
	return Payload{ExternalID: externalID, State: state, Error: errState, Metadata: metadata, Artifacts: artifacts}
}

func MarshalPayload(p Payload) (string, error) {
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

	if p.ExternalID == 0 && p.State == "" {
		return Payload{}, errors.New("unmarshal payload: taskId is zero")
	}

	return p, nil
}
