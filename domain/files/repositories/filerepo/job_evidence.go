package filerepo

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"

	"github.com/assurrussa/gouploads/domain/files/model"
)

const fileJobEvidenceLimit = 100

// GetFileJobs uses one PostgreSQL statement snapshot for links, current file
// binding, active jobs and terminal failures. Verified against pinned Outbox
// PostgreSQL v0.12.0: jobs_failed.job_id is the original ID; .id is NOT.
// It never reads payloads, failure text, storage, or lease-token values.
func (r *Repo) GetFileJobs(ctx context.Context, fileID int64, operation model.FileJobOperation) (model.FileJobSnapshot, error) {
	if fileID <= 0 || !operation.Valid() {
		return model.FileJobSnapshot{}, errors.New("invalid file job diagnostic request")
	}
	const query = `with links as (
    select *, xmin::text as link_revision from file_job_associations
    where file_id = $1 and operation = $2
    order by created_at desc, job_id desc limit 101
)
select statement_timestamp(),
    coalesce(l.job_id::text, ''), coalesce(l.job_name, ''),
    coalesce(l.schema_version, 0),
    coalesce(json_build_array(l.file_id, l.generation, l.object_type, l.object_id)::text, ''),
    case
        when f.id is null then 'file_missing'
        when f.deleted_at is not null then 'file_deleted'
        when f.slug = l.generation and f.object_type = l.object_type
            and f.object_id is not distinct from l.object_id then 'current'
        else 'mismatch'
    end,
    case
        when j.id is not null and d.id is not null then 'unknown'
        when j.id is not null and
            (j.name <> l.job_name or j.schema_version <> l.schema_version) then 'unknown'
        when d.id is not null and
            (d.name <> l.job_name or d.schema_version <> l.schema_version) then 'unknown'
        when d.id is not null then 'terminal_failed'
        when j.id is null then 'unknown'
        when j.reserved_at is not null and
            j.lease_token = '00000000-0000-0000-0000-000000000000'::uuid then 'unknown'
        when j.reserved_at > statement_timestamp() then 'leased'
        when j.reserved_at is not null then 'lease_expired'
        when j.available_at > statement_timestamp() then 'delayed'
        else 'available'
    end,
    j.attempts,
    concat_ws(':', l.link_revision, j.xmin::text, d.xmin::text, f.xmin::text)
from (select $1::bigint as id) requested
left join links l on true
left join files f on f.id = requested.id
left join jobs j on j.id = l.job_id
left join jobs_failed d on d.job_id = l.job_id
order by l.created_at desc, l.job_id desc`
	rows, err := r.pgsql.DB().Query(ctx, "filerepo.GetFileJobs", query, fileID, string(operation))
	if err != nil {
		return model.FileJobSnapshot{}, err
	}
	defer rows.Close()
	result := model.FileJobSnapshot{Jobs: []model.FileJobEvidence{}}
	for rows.Next() {
		var job model.FileJobEvidence
		var binding, revision string
		if err := rows.Scan(&result.ObservedAt, &job.JobID, &job.Name, &job.SchemaVersion,
			&binding, &job.Binding, &job.State, &job.Attempts, &revision); err != nil {
			return model.FileJobSnapshot{}, err
		}
		if job.JobID == "" {
			continue
		}
		if len(result.Jobs) == fileJobEvidenceLimit {
			result.Truncated = true
			continue
		}
		job.Generation = fileJobReference(binding)
		job.Revision = fileJobReference(job.JobID + ":" + revision + ":" + string(job.State))
		result.Jobs = append(result.Jobs, job)
	}
	if err := rows.Err(); err != nil {
		return model.FileJobSnapshot{}, err
	}
	return result, nil
}

func fileJobReference(value string) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:])
}
