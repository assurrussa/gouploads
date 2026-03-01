package shared

import (
	"errors"

	"github.com/assurrussa/goshared/pkg/sharedtypes"
)

const EventTypeUploadStatus = "files.upload.status"

type FileUploadEventFile struct {
	ID           int64  `json:"id"`
	FileName     string `json:"fileName"`
	OriginalName string `json:"originalName"`
	URL          string `json:"url"`
	Size         int64  `json:"size"`
	MimeType     string `json:"mimeType"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
	IsPrimary    bool   `json:"isPrimary"`
}

type FileUploadStatusEvent struct {
	ID          sharedtypes.EventID  `json:"eventId"`
	EventType   string               `json:"eventType"`
	TaskID      int64                `json:"taskId"`
	Status      FileUploadTaskStatus `json:"status"`
	File        *FileUploadEventFile `json:"file,omitempty"`
	Error       string               `json:"error,omitempty"`
	TempFileURL string               `json:"tempFileUrl,omitempty"`
	Metadata    map[string]any       `json:"metadata,omitempty"`
}

func NewFileUploadStatusEvent(taskID int64, status FileUploadTaskStatus) FileUploadStatusEvent {
	return FileUploadStatusEvent{
		ID:        sharedtypes.NewEventID(),
		EventType: EventTypeUploadStatus,
		TaskID:    taskID,
		Status:    status,
	}
}

func (e FileUploadStatusEvent) EventID() sharedtypes.EventID {
	return e.ID
}

func (e FileUploadStatusEvent) EventName() string {
	return EventTypeUploadStatus
}

func (e FileUploadStatusEvent) Validate() error {
	if e.TaskID <= 0 {
		return errors.New("taskID must be positive")
	}
	if e.Status == "" {
		return errors.New("status is required")
	}
	if e.EventType == "" {
		return errors.New("eventType is required")
	}
	return nil
}
