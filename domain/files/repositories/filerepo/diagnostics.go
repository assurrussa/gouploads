package filerepo

import (
	"context"
	"errors"

	"github.com/assurrussa/gouploads/domain/files/model"
)

// GetFileLifecycle reads one database snapshot, including soft-deleted rows and
// retained tombstones. It does not lock rows, load payloads or inspect storage.
func (r *Repo) GetFileLifecycle(ctx context.Context, id int64) (model.FileLifecycleSnapshot, error) {
	if id <= 0 {
		return model.FileLifecycleSnapshot{}, errors.New("filerepo.GetFileLifecycle: invalid id")
	}
	const query = `select
    f.id is not null,
    coalesce(f.deleted_at is not null, false),
    case
        when f.data->'fileUploader'->>'status' in ('queued', 'processing', 'completed', 'failed')
            then f.data->'fileUploader'->>'status'
        when coalesce(f.data->'fileUploader'->>'status', '') = '' then ''
        else 'unknown'
    end,
    coalesce(jsonb_typeof(f.data->'presets') = 'object' and f.data->'presets' <> '{}'::jsonb, false),
    exists(select 1 from upload_finalizations where file_id = $1),
    d.file_id is not null,
    d.completed_at is not null
from (select $1::bigint as id) requested
left join files f on f.id = requested.id
left join file_deletions d on d.file_id = requested.id`
	var snapshot model.FileLifecycleSnapshot
	err := r.pgsql.DB().QueryRow(ctx, "filerepo.GetFileLifecycle", query, id).Scan(
		&snapshot.FileExists, &snapshot.FileDeleted, &snapshot.UploadStatus,
		&snapshot.HasPresets, &snapshot.FinalizationRecorded,
		&snapshot.DeletionPlanned, &snapshot.DeletionCompleted,
	)
	if err != nil {
		return model.FileLifecycleSnapshot{}, err
	}
	return snapshot, nil
}
