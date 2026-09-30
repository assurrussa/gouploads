package deletefile

import (
	"context"

	"github.com/assurrussa/gouploads/domain/files/model"
)

// Kept separate from the generated legacy port. Custom repositories must now
// implement this durable capability; refusing deletion is safer than losing data.
type deletionPlanner interface {
	GetDeletionPlanForUpdate(ctx context.Context, id int64) (model.DeletionPlan, bool, error)
	SaveDeletionPlan(ctx context.Context, plan model.DeletionPlan) error
	CompleteDeletionPlan(ctx context.Context, id int64) error
}
