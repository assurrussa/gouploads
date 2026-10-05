package sendresizefilejob

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/assurrussa/gouploads/domain/files/shared"
)

type Payload = shared.MediaDispatchPayload

func NewPayload(
	fileID int64,
	filePath string,
	skipResizeVideo bool,
) Payload {
	return Payload{
		FileID:          fileID,
		FilePath:        filePath,
		SkipResizeVideo: skipResizeVideo,
	}
}

func MarshalPayload(p Payload) (string, error) {
	if p.FileID == 0 && p.FilePath == "" {
		return "", errors.New("unmarshal payload: taskId is zero")
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

	if p.FileID == 0 && p.FilePath == "" {
		return Payload{}, errors.New("unmarshal payload: taskId is zero")
	}

	return p, nil
}
