package host

import (
	"context"
	"errors"
	"time"

	"github.com/assurrussa/gouploads/domain/files/model"
)

// ErrInvalidDiagnosticCutoff identifies invalid caller-supplied what-if timestamps.
var ErrInvalidDiagnosticCutoff = errors.New("diagnostic cutoff must be a nonzero timestamp in years 1 through 9999")

type (
	// DeletionRetentionSnapshot contains aggregate evidence without identity values or plans.
	DeletionRetentionSnapshot = model.DeletionRetentionSnapshot
	// DeletionRetentionProjection describes a caller-supplied age what-if, never mutation eligibility.
	DeletionRetentionProjection = model.DeletionRetentionProjection
)

// DeletionRetentionReader is satisfied by FileRepo. Hosts must authorize the
// entire scope being inventoried, or supply their own appropriately scoped reader.
type DeletionRetentionReader interface {
	GetDeletionRetention(ctx context.Context, completedBefore *time.Time) (DeletionRetentionSnapshot, error)
}

// DeletionRetentionInventory distinguishes unavailable evidence from an empty
// successful read. Snapshot is present only when Source is DiagnosticPresent.
type DeletionRetentionInventory struct {
	Source   DiagnosticState            `json:"source"`
	Snapshot *DeletionRetentionSnapshot `json:"snapshot,omitempty"`
}

// InspectDeletionRetention performs one read, with no policy, schedule or route.
// Nil cutoff omits the projection; otherwise completed_at < cutoff is a what-if.
// A FileRepo reads all deletion plans in its database scope. The host owns access
// control, cancellation and cost limits. SQL errors are sanitized like DiagnoseFile.
func InspectDeletionRetention(
	ctx context.Context, reader DeletionRetentionReader, completedBefore *time.Time,
) (DeletionRetentionInventory, error) {
	result := DeletionRetentionInventory{Source: DiagnosticUnavailable}
	var cutoff *time.Time
	if completedBefore != nil {
		if completedBefore.IsZero() || completedBefore.UTC().Year() < 1 || completedBefore.UTC().Year() > 9999 {
			return result, ErrInvalidDiagnosticCutoff
		}
		value := completedBefore.UTC()
		cutoff = &value
	}
	if err := ctx.Err(); err != nil {
		return result, err
	}
	if nilRuntimeDependency(reader) {
		return result, ErrDiagnosticUnavailable
	}
	snapshot, err := reader.GetDeletionRetention(ctx, cutoff)
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
	result.Source = DiagnosticPresent
	result.Snapshot = &snapshot
	return result, nil
}
