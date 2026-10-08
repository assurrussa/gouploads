// Package filejobs carries explicit producer provenance without inspecting payloads.
package filejobs

import (
	"context"
	"time"

	"github.com/assurrussa/outbox/shared/types"

	"github.com/assurrussa/gouploads/domain/files/model"
)

type Putter interface {
	Put(ctx context.Context, name, payload string, availableAt time.Time) (types.JobID, error)
}

// AssociatedPutter is opt-in. Implementations must commit the queue row and
// immutable file/operation association on the SAME transaction and connection.
// A callback to an arbitrary Putter cannot establish this guarantee.
type AssociatedPutter interface {
	PutFileJob(ctx context.Context, file model.File, operation model.FileJobOperation,
		name, payload string, availableAt time.Time,
	) (types.JobID, error)
}

// Put preserves custom/legacy queues. Their jobs remain explicitly unmapped.
// Empty-generation legacy files and staging-only jobs are never guessed.
func Put(ctx context.Context, queue Putter, file model.File, operation model.FileJobOperation,
	name, payload string, availableAt time.Time,
) (types.JobID, error) {
	if file.ID > 0 && file.Slug != "" {
		if associated, ok := queue.(AssociatedPutter); ok {
			return associated.PutFileJob(ctx, file, operation, name, payload, availableAt)
		}
	}
	return queue.Put(ctx, name, payload, availableAt)
}
