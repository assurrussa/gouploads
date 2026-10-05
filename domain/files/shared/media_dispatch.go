package shared

import (
	"time"

	outboxtypes "github.com/assurrussa/outbox/shared/types"
)

// MediaDispatchPayload remains in send_resize_file until admission is known,
// then carries a persisted polling continuation. Dispatch retries never change
// the file-ID idempotency key and polling never resubmits the original request.
type MediaDispatchPayload struct {
	FileID          int64              `json:"fileId"`
	FilePath        string             `json:"filePath"`
	SkipResizeVideo bool               `json:"skipResize"`
	JobID           *outboxtypes.JobID `json:"jobId,omitempty"`
	PollDeadline    *time.Time         `json:"pollDeadline,omitempty"`
}
