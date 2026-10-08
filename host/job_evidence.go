package host

import (
	"context"
	"errors"

	"github.com/assurrussa/gouploads/domain/files/model"
)

type FileJobOperation = model.FileJobOperation
type FileJobEvidence = model.FileJobEvidence
type FileJobSnapshot = model.FileJobSnapshot
type FileJobState = model.FileJobState

const (
	FileJobOriginalFinalization = model.FileJobOriginalFinalization
	FileJobMediaAdmission       = model.FileJobMediaAdmission
	FileJobMediaFinalization    = model.FileJobMediaFinalization
	FileJobDeletion             = model.FileJobDeletion
	FileJobUnknown              = model.FileJobUnknown
	FileJobAvailable            = model.FileJobAvailable
	FileJobDelayed              = model.FileJobDelayed
	FileJobLeased               = model.FileJobLeased
	FileJobLeaseExpired         = model.FileJobLeaseExpired
	FileJobFailed               = model.FileJobFailed
)

var ErrInvalidDiagnosticOperation = errors.New("invalid diagnostic operation")

// FileJobReader must read only explicitly persisted associations and queue metadata.
type FileJobReader interface {
	GetFileJobs(context.Context, int64, FileJobOperation) (FileJobSnapshot, error)
}

// FileJobDiagnosis reports partial evidence only. No complete-history or
// successful-acknowledgment retention contract exists in the pinned Outbox.
type FileJobDiagnosis struct {
	FileID     int64            `json:"fileId"`
	Operation  FileJobOperation `json:"operation"`
	Coverage   string           `json:"coverage"`
	Outcome    string           `json:"outcome"`
	Quiescence string           `json:"quiescence"`
	FileJobSnapshot
}

// InspectFileJobs installs no route and performs no mutation. Authorize the
// FileID and retained historical ownership in the host before calling.
// An empty set is historical/unmapped; absent jobs and expired leases are
// unknown outcomes, never permission to retry/delete.
func InspectFileJobs(ctx context.Context, reader FileJobReader, fileID int64, operation FileJobOperation) (FileJobDiagnosis, error) {
	result := FileJobDiagnosis{FileID: fileID, Operation: operation, Coverage: "unavailable",
		Outcome: "unknown", Quiescence: "unknown"}
	if fileID <= 0 {
		return result, ErrInvalidDiagnosticFileID
	}
	if !operation.Valid() {
		return result, ErrInvalidDiagnosticOperation
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if nilRuntimeDependency(reader) {
		return result, ErrDiagnosticUnavailable
	}
	snapshot, err := reader.GetFileJobs(ctx, fileID, operation)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return result, ctxErr
		}
		if errors.Is(err, context.Canceled) {
			return result, context.Canceled
		}
		if errors.Is(err, context.DeadlineExceeded) {
			return result, context.DeadlineExceeded
		}
		return result, ErrDiagnosticUnavailable
	}
	result.FileJobSnapshot = snapshot
	result.Coverage = "partial"
	if len(snapshot.Jobs) == 0 && !snapshot.Truncated {
		result.Coverage = "historical_unmapped"
	}
	return result, nil
}
