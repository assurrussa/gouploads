package host

import (
	"context"
	"errors"

	"github.com/assurrussa/gouploads/domain/files/model"
)

var (
	ErrInvalidDiagnosticFileID = errors.New("diagnostic file ID must be positive")
	ErrDiagnosticUnavailable   = errors.New("file diagnostic source unavailable")
)

// FileLifecycleSnapshot is the path-free evidence returned by FileRepo.
type FileLifecycleSnapshot = model.FileLifecycleSnapshot

// FileDiagnosticReader is satisfied by FileRepo. Hosts may supply an authorized
// reader scoped to their tenant. It must only read lifecycle evidence.
type FileDiagnosticReader interface {
	GetFileLifecycle(ctx context.Context, fileID int64) (FileLifecycleSnapshot, error)
}

// DiagnosticState is a normalized observation from a lifecycle source.
type DiagnosticState string

const (
	DiagnosticUnknown     DiagnosticState = "unknown"
	DiagnosticUnavailable DiagnosticState = "unavailable"
	DiagnosticMissing     DiagnosticState = "missing"
	DiagnosticPresent     DiagnosticState = "present"
	DiagnosticDeleted     DiagnosticState = "deleted"
	DiagnosticNone        DiagnosticState = "none"
	DiagnosticRecorded    DiagnosticState = "recorded"
	DiagnosticQueued      DiagnosticState = "queued"
	DiagnosticProcessing  DiagnosticState = "processing"
	DiagnosticCompleted   DiagnosticState = "completed"
	DiagnosticFailed      DiagnosticState = "failed"
	DiagnosticDeleting    DiagnosticState = "deleting"
)

// FileDiagnosis reports independent observations, not a recovery decision.
// Job is unavailable because UploadOutbox has no job-state lookup contract.
type FileDiagnosis struct {
	FileID       int64           `json:"fileId"`
	File         DiagnosticState `json:"file"`
	Upload       DiagnosticState `json:"upload"`
	Finalization DiagnosticState `json:"finalization"`
	Deletion     DiagnosticState `json:"deletion"`
	Job          DiagnosticState `json:"job"`
}

// DiagnoseFile performs one read. The host must authorize the FileID before
// calling; no route or authorization policy is installed by this function.
// Source errors are deliberately sanitized; errors.Is supports the sentinels
// and context cancellation without disclosing database details.
func DiagnoseFile(ctx context.Context, reader FileDiagnosticReader, fileID int64) (FileDiagnosis, error) {
	if fileID <= 0 {
		return FileDiagnosis{}, ErrInvalidDiagnosticFileID
	}
	result := FileDiagnosis{
		FileID: fileID, File: DiagnosticUnavailable, Upload: DiagnosticUnavailable,
		Finalization: DiagnosticUnavailable, Deletion: DiagnosticUnavailable, Job: DiagnosticUnavailable,
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if nilRuntimeDependency(reader) {
		return result, ErrDiagnosticUnavailable
	}
	snapshot, err := reader.GetFileLifecycle(ctx, fileID)
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
	result.File = DiagnosticMissing
	result.Upload = DiagnosticUnknown
	if snapshot.FileExists {
		result.File = DiagnosticPresent
		if snapshot.FileDeleted {
			result.File = DiagnosticDeleted
		}
		result.Upload = diagnosticUploadState(snapshot)
	}
	result.Finalization = DiagnosticNone
	if snapshot.FinalizationRecorded {
		result.Finalization = DiagnosticRecorded
	}
	result.Deletion = DiagnosticNone
	if snapshot.DeletionPlanned {
		result.Deletion = DiagnosticDeleting
		if snapshot.DeletionCompleted {
			result.Deletion = DiagnosticCompleted
		}
	}
	return result, nil
}

func diagnosticUploadState(snapshot FileLifecycleSnapshot) DiagnosticState {
	switch snapshot.UploadStatus {
	case FileUploadTaskStatusQueued:
		return DiagnosticQueued
	case FileUploadTaskStatusProcessing:
		return DiagnosticProcessing
	case FileUploadTaskStatusCompleted:
		return DiagnosticCompleted
	case FileUploadTaskStatusFailed:
		return DiagnosticFailed
	case "":
		// Match File.IsUploadCompleted for legacy finalized metadata.
		if snapshot.HasPresets {
			return DiagnosticCompleted
		}
	}
	return DiagnosticUnknown
}
