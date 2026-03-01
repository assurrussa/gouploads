package shared

import (
	"errors"

	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
)

const EventTypeAfterProcess = "file.after.process"

type Status string

const (
	StatusCompleted Status = "completed"
	StatusFailed    Status = "failed"
)

type EventAfterProcess struct {
	ID           sharedtypes.EventID `json:"id"`
	EventType    string              `json:"eventType"`
	FileID       int64               `json:"fileId"`
	FilePath     string              `json:"filePath"`
	Status       Status              `json:"status"`
	EventTrigger string              `json:"eventTrigger"`
	Error        string              `json:"error"`
}

func NewEventAfterProcess(fileID int64, filePath string, status Status, eventTrigger string) EventAfterProcess {
	return EventAfterProcess{
		ID:           sharedtypes.NewEventID(),
		EventType:    EventTypeAfterProcess,
		FileID:       fileID,
		Status:       status,
		FilePath:     filePath,
		EventTrigger: eventTrigger,
	}
}

func (e EventAfterProcess) EventID() sharedtypes.EventID {
	return e.ID
}

func (e EventAfterProcess) EventName() string {
	return EventTypeAfterProcess
}

func (e EventAfterProcess) Validate() error {
	if e.Status == "" {
		return errors.New("status is required")
	}
	if e.EventTrigger == "" {
		return errors.New("trigger is required")
	}
	return nil
}

// FileEventAfterJob describes a job to be scheduled after a successful upload.
type FileEventAfterJob struct {
	JobName string             `json:"jobName"`
	UserID  sharedtypes.UserID `json:"userId"`
	Payload string             `json:"payload"`
	Meta    map[string]any     `json:"meta,omitempty"`
}

func NewFileEventAfterJob(jobName string, userID sharedtypes.UserID, payload string, metas ...map[string]any) FileEventAfterJob {
	var meta map[string]any
	for _, m := range metas {
		if m != nil {
			meta = m
			break
		}
	}

	return FileEventAfterJob{JobName: jobName, UserID: userID, Payload: payload, Meta: meta}
}

func NewFileEventAfterJobs(jobName string, userID sharedtypes.UserID, metas ...map[string]any) []FileEventAfterJob {
	return []FileEventAfterJob{NewFileEventAfterJob(jobName, userID, "", metas...)}
}

func NewFileEventAfterJobsWithPayload(jobName string, userID sharedtypes.UserID, payload string) []FileEventAfterJob {
	return []FileEventAfterJob{NewFileEventAfterJob(jobName, userID, payload)}
}
