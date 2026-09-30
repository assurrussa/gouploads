package filerepo

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"

	"github.com/assurrussa/gouploads/domain/files/model"
)

// FinalizeUpload serializes the entire application handoff, including creation
// of File and its outbox job. create must use the supplied transaction context.
// The minimal key tombstone outlives file deletion so a retry cannot resurrect it.
func (r *Repo) FinalizeUpload(
	ctx context.Context, key, binding string, requireSession bool,
	create func(context.Context) (model.File, error),
) (model.File, error) {
	parsed, err := uuid.Parse(key)
	if err != nil || parsed == uuid.Nil || parsed.String() != key {
		return model.File{}, errors.New("canonical finalization UUID is required")
	}
	if len(binding) != 64 || create == nil {
		return model.File{}, errors.New("finalization binding and callback are required")
	}
	var result model.File
	err = r.trxManager.RunInTx(ctx, func(ctx context.Context) error {
		// The S3 cleanup claim takes the very same lock before claiming a session.
		if _, err := r.pgsql.DB().Exec(ctx, "filerepo.LockFinalization",
			"select pg_advisory_xact_lock(hashtextextended('gouploads:finalize:' || $1, 0))", key); err != nil {
			return err
		}
		var found bool
		result, found, err = r.findFinalizedUpload(ctx, key, binding)
		if err != nil || found {
			return err
		}
		if requireSession {
			if err := r.lockReadyUploadSession(ctx, key); err != nil {
				return err
			}
		}
		result, err = create(ctx)
		if err != nil {
			return err
		}
		if result.ID <= 0 {
			return errors.New("finalization callback returned an empty file")
		}
		_, err = r.pgsql.DB().Exec(ctx, "filerepo.SaveFinalization",
			"insert into upload_finalizations (finalization_key, binding_hash, file_id) values ($1::uuid, $2, $3)",
			key, binding, result.ID,
		)
		return err
	})
	if err != nil {
		return model.File{}, err
	}
	return result, nil
}

func (r *Repo) findFinalizedUpload(ctx context.Context, key, binding string) (model.File, bool, error) {
	var fileID int64
	var savedBinding string
	err := r.pgsql.DB().QueryRow(ctx, "filerepo.FindFinalization",
		"select file_id, binding_hash from upload_finalizations where finalization_key = $1::uuid", key,
	).Scan(&fileID, &savedBinding)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.File{}, false, nil
	}
	if err != nil {
		return model.File{}, false, fmt.Errorf("find finalization: %w", err)
	}
	if savedBinding != binding {
		return model.File{}, true, model.ErrFinalizationConflict
	}
	file, err := r.GetByID(ctx, fileID)
	if err != nil {
		return model.File{}, true, err
	}
	if file.ID == 0 {
		return model.File{}, true, model.ErrFinalizationGone
	}
	return file, true, nil
}

func (r *Repo) lockReadyUploadSession(ctx context.Context, key string) error {
	var status string
	err := r.pgsql.DB().QueryRow(ctx, "filerepo.LockUploadSession",
		"select status from upload_sessions where finalization_key = $1::uuid for update", key,
	).Scan(&status)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.ErrFinalizationGone
	}
	if err != nil {
		return err
	}
	if status != "ready" {
		return model.ErrFinalizationConflict
	}
	return nil
}

// GetDeletionPlanForUpdate must be used inside the caller's transaction.
func (r *Repo) GetDeletionPlanForUpdate(ctx context.Context, id int64) (model.DeletionPlan, bool, error) {
	var body []byte
	var completed bool
	err := r.pgsql.DB().QueryRow(ctx, "filerepo.GetDeletionPlan",
		"select payload, completed_at is not null from file_deletions where file_id = $1 for update", id).Scan(&body, &completed)
	if errors.Is(err, pgx.ErrNoRows) {
		return model.DeletionPlan{}, false, nil
	}
	if err != nil {
		return model.DeletionPlan{}, false, err
	}
	var plan model.DeletionPlan
	if err := json.Unmarshal(body, &plan); err != nil {
		return model.DeletionPlan{}, false, fmt.Errorf("decode deletion plan: %w", err)
	}
	if plan.File.ID != id {
		return model.DeletionPlan{}, false, errors.New("deletion plan file ID mismatch")
	}
	plan.Completed = completed
	return plan, true, nil
}

func (r *Repo) SaveDeletionPlan(ctx context.Context, plan model.DeletionPlan) error {
	if plan.File.ID <= 0 {
		return errors.New("deletion plan requires a file ID")
	}
	body, err := json.Marshal(plan)
	if err != nil {
		return err
	}
	_, err = r.pgsql.DB().Exec(ctx, "filerepo.SaveDeletionPlan",
		"insert into file_deletions (file_id, payload) values ($1, $2::jsonb)", plan.File.ID, body)
	return err
}

func (r *Repo) CompleteDeletionPlan(ctx context.Context, id int64) error {
	tag, err := r.pgsql.DB().Exec(ctx, "filerepo.CompleteDeletionPlan",
		"update file_deletions set completed_at = clock_timestamp() where file_id = $1 and completed_at is null", id)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return errors.New("deletion plan is missing or already completed")
	}
	return nil
}
