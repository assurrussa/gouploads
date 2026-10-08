package model

import "time"

// FileJobOperation identifies a producer-owned operation, not a recovery action.
type FileJobOperation string

const (
	FileJobOriginalFinalization FileJobOperation = "original_finalization"
	FileJobMediaAdmission       FileJobOperation = "media_admission"
	FileJobMediaFinalization    FileJobOperation = "media_finalization"
	FileJobDeletion             FileJobOperation = "deletion"
)

func (op FileJobOperation) Valid() bool {
	switch op {
	case FileJobOriginalFinalization, FileJobMediaAdmission, FileJobMediaFinalization, FileJobDeletion:
		return true
	default:
		return false
	}
}

type FileJobState string

const (
	FileJobUnknown      FileJobState = "unknown"
	FileJobAvailable    FileJobState = "available"
	FileJobDelayed      FileJobState = "delayed"
	FileJobLeased       FileJobState = "leased"
	FileJobLeaseExpired FileJobState = "lease_expired"
	FileJobFailed       FileJobState = "terminal_failed"
)

// FileJobEvidence intentionally omits payloads, paths, lease tokens and DLQ errors.
// Generation and Revision are opaque observation references, never action tokens.
type FileJobEvidence struct {
	JobID         string       `json:"jobId"`
	Name          string       `json:"name"`
	SchemaVersion int          `json:"schemaVersion"`
	Generation    string       `json:"generation"`
	Binding       string       `json:"binding"`
	State         FileJobState `json:"state"`
	Attempts      *int         `json:"attempts,omitempty"`
	Revision      string       `json:"revision"`
}

type FileJobSnapshot struct {
	ObservedAt time.Time         `json:"observedAt"`
	Jobs       []FileJobEvidence `json:"jobs"`
	Truncated  bool              `json:"truncated"`
}
