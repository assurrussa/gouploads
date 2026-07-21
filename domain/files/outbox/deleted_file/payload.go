package deletedfilejob

import (
	"encoding/json"
	"errors"
	"fmt"

	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"

	"github.com/assurrussa/gouploads/domain/files/shared"
)

type Payload struct {
	UserID      sharedtypes.UserID         `json:"userId"`
	FileID      int64                      `json:"fileId"`
	FilePath    string                     `json:"filepath"`
	ObjectType  shared.FileObjectType      `json:"objectType,omitempty"`
	ObjectID    shared.FileObjectID        `json:"objectId,omitempty"`
	AfterEvents []shared.FileEventAfterJob `json:"afterEvents"`
}

func NewPayload(fileID int64, userID sharedtypes.UserID, filePath string, afterEvents ...shared.FileEventAfterJob) Payload {
	return Payload{UserID: userID, FileID: fileID, FilePath: filePath, AfterEvents: afterEvents}
}

func NewOwnedPayload(
	fileID int64,
	userID sharedtypes.UserID,
	objectType shared.FileObjectType,
	objectID shared.FileObjectID,
	filePath string,
	afterEvents ...shared.FileEventAfterJob,
) Payload {
	return Payload{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    filePath,
		ObjectType:  objectType,
		ObjectID:    objectID,
		AfterEvents: afterEvents,
	}
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

	if p.FileID == 0 && p.FilePath == "" {
		return Payload{}, errors.New("unmarshal payload: taskId is zero")
	}

	return p, nil
}
