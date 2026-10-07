package filerepo

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/assurrussa/gouploads/domain/files/model"
)

// GetDeletionRetention reads aggregate deletion-plan evidence in one statement.
// It neither decodes plans into Go nor locks, mutates or calls external systems.
func (r *Repo) GetDeletionRetention(ctx context.Context, completedBefore *time.Time) (model.DeletionRetentionSnapshot, error) {
	var cutoff any
	var cutoffUTC time.Time
	if completedBefore != nil {
		if completedBefore.IsZero() || completedBefore.UTC().Year() < 1 || completedBefore.UTC().Year() > 9999 {
			return model.DeletionRetentionSnapshot{}, errors.New("filerepo.GetDeletionRetention: invalid cutoff")
		}
		cutoffUTC = completedBefore.UTC()
		cutoff = cutoffUTC
	}
	const query = `with evidence as (
 select completed_at, created_at,
        octet_length(payload::text)::bigint as bytes,
        coalesce(file_id > 0 and jsonb_typeof(payload) = 'object'
            and jsonb_typeof(payload->'file') = 'object'
            and jsonb_typeof(payload->'file'->'id') = 'number'
            and payload->'file'->>'id' = file_id::text, false) as envelope_known,
        coalesce(jsonb_typeof(payload->'file'->'objectType') = 'string'
            and (payload->'file'->>'objectType') collate "C" ~ '^[a-z][a-z0-9_]{0,63}$'
            and jsonb_typeof(payload->'file'->'objectId') = 'number'
            and (payload->'file'->>'objectId') collate "C" ~ '^[1-9][0-9]{0,18}$'
            and (length(payload->'file'->>'objectId') < 19
                 or (payload->'file'->>'objectId') collate "C" <= '9223372036854775807'), false) as binding_known,
        isfinite(created_at) and created_at >= timestamptz '0001-01-01 00:00:00+00'
            and created_at <= statement_timestamp()
            and (completed_at is null or (isfinite(completed_at)
                and completed_at <= statement_timestamp() and completed_at >= created_at)) as age_known
 from file_deletions
)
select statement_timestamp(), count(*),
 count(*) filter (where completed_at is null),
 count(*) filter (where completed_at is not null),
 count(*) filter (where not envelope_known),
 count(*) filter (where envelope_known and not binding_known),
 count(*) filter (where not age_known),
 coalesce(sum(bytes), 0)::bigint,
 coalesce(sum(bytes) filter (where completed_at is null), 0)::bigint,
 coalesce(sum(bytes) filter (where completed_at is not null), 0)::bigint,
 min(created_at) filter (where completed_at is null and age_known),
 min(completed_at) filter (where completed_at is not null and age_known),
 count(*) filter (where age_known and completed_at < $1::timestamptz),
 coalesce(sum(bytes) filter (where age_known and completed_at < $1::timestamptz), 0)::bigint
from evidence`
	var snapshot model.DeletionRetentionSnapshot
	var pendingAt, completedAt sql.NullTime
	var projectedCount, projectedBytes int64
	err := r.pgsql.DB().QueryRow(ctx, "filerepo.GetDeletionRetention", query, cutoff).Scan(
		&snapshot.ObservedAt, &snapshot.TotalCount, &snapshot.PendingCount, &snapshot.CompletedCount,
		&snapshot.MalformedEnvelopeCount, &snapshot.UnrecognizedBindingCount, &snapshot.UnknownAgeCount,
		&snapshot.PayloadJSONTextBytesEstimate, &snapshot.PendingPayloadJSONTextBytesEstimate,
		&snapshot.CompletedPayloadJSONTextBytesEstimate, &pendingAt, &completedAt,
		&projectedCount, &projectedBytes,
	)
	if err != nil {
		return model.DeletionRetentionSnapshot{}, err
	}
	snapshot.ObservedAt = snapshot.ObservedAt.UTC()
	if pendingAt.Valid {
		value := pendingAt.Time.UTC()
		snapshot.OldestPendingCreatedAt = &value
	}
	if completedAt.Valid {
		value := completedAt.Time.UTC()
		snapshot.OldestCompletedAt = &value
	}
	if completedBefore != nil {
		snapshot.Projection = &model.DeletionRetentionProjection{
			CompletedBefore: cutoffUTC, CompletedCount: projectedCount,
			PayloadJSONTextBytesEstimate: projectedBytes,
		}
	}
	return snapshot, nil
}
