package shared

import (
	"errors"

	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

const EventTypeDeleted = "file.deleted"

type FileDeleteStatus string

const (
	FileDeleteStatusCompleted FileDeleteStatus = "completed"
	FileDeleteStatusFailed    FileDeleteStatus = "failed"
)

type FileDeletedEvent struct {
	ID        sharedtypes.EventID `json:"id"`
	EventType string              `json:"eventType"`
	FileID    int64               `json:"fileId"`
	FilePath  string              `json:"filePath"`
	Status    FileDeleteStatus    `json:"status"`
	Error     string              `json:"error"`
}

func NewFileDeletedEvent(fileID int64, filePath string, status FileDeleteStatus) FileDeletedEvent {
	return FileDeletedEvent{
		ID:        sharedtypes.NewEventID(),
		EventType: EventTypeDeleted,
		FileID:    fileID,
		Status:    status,
		FilePath:  filePath,
	}
}

func (e FileDeletedEvent) EventID() sharedtypes.EventID {
	return e.ID
}

func (e FileDeletedEvent) EventName() string {
	return EventTypeDeleted
}

func (e FileDeletedEvent) Validate() error {
	// if e.FileID <= 0 {
	//	return errors.New("FileID must be positive")
	// }
	if e.Status == "" {
		return errors.New("status is required")
	}
	return nil
}
