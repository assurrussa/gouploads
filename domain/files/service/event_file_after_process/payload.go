package eventfileafterprocess

import (
	"encoding/json"
	"errors"
	"fmt"

	"github.com/assurrussa/gouploads/domain/files/shared"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

type Payload struct {
	UserID    sharedtypes.UserID `json:"userId"`
	UserType  shared.UserType    `json:"userType"`
	FileID    int64              `json:"fileId"`
	EventType string             `json:"eventType"`
	Meta      map[string]any     `json:"meta"`
}

func NewPayload(
	userID sharedtypes.UserID,
	userType shared.UserType,
	fileID int64,
	eventType string,
	metas ...map[string]any,
) Payload {
	var meta map[string]any
	if len(metas) > 0 {
		meta = metas[0]
	}

	return Payload{UserID: userID, UserType: userType, FileID: fileID, EventType: eventType, Meta: meta}
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

	if p.FileID == 0 && p.UserID.IsZero() {
		return Payload{}, errors.New("unmarshal payload: taskId or userId is zero")
	}

	return p, nil
}
