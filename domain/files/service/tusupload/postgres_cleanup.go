package tusupload

import (
	"context"
	"errors"
	"time"

	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
)

func (r *postgresSessionRepository) TouchReady(ctx context.Context, id string, ttl time.Duration) (s3Session, error) {
	query := `update upload_sessions
set expires_at = greatest(expires_at, clock_timestamp() + ($2 * interval '1 millisecond')),
    updated_at = clock_timestamp()
where id = $1::uuid and status = 'ready'
returning ` + sessionColumns
	result, err := scanSession(r.db.DB().QueryRow(ctx, "tus_session_touch_ready", query, id, durationMilliseconds(ttl)))
	if errors.Is(err, ErrNotFound) {
		return s3Session{}, ErrUploadBusy
	}
	return result, err
}

func (r *postgresSessionRepository) ClaimCleanup(
	ctx context.Context, id string, before time.Time, owner string, ttl time.Duration,
) (cleanupClaim, error) {
	var result cleanupClaim
	err := transaction.New(r.db.DB()).RunInTx(ctx, func(ctx context.Context) error {
		current, err := r.Get(ctx, id)
		if err != nil {
			return err
		}
		// Serialize cleanup with the application handoff, not just PATCH.
		if _, err := r.db.DB().Exec(ctx, "tus_session_cleanup_handoff_lock",
			"select pg_advisory_xact_lock(hashtextextended('gouploads:finalize:' || $1, 0))", current.FinalizationKey); err != nil {
			return err
		}
		query := `update upload_sessions
set status = 'cleaning', lease_owner = $2::uuid,
    lease_until = clock_timestamp() + ($3 * interval '1 millisecond'),
    revision = revision + 1, updated_at = clock_timestamp()
where id = $1::uuid
  and (lease_until is null or lease_until <= clock_timestamp())
  and ($4::boolean or expires_at < $5)
returning ` + sessionColumns
		session, err := scanSession(r.db.DB().QueryRow(
			ctx, "tus_session_claim_cleanup", query, id, owner, durationMilliseconds(ttl), before.IsZero(), before,
		))
		if errors.Is(err, ErrNotFound) {
			return ErrUploadBusy
		}
		if err != nil {
			return err
		}
		var keep bool
		row := r.db.DB().QueryRow(ctx, "tus_session_cleanup_handoff",
			"select exists(select 1 from upload_finalizations where finalization_key = $1::uuid)", session.FinalizationKey,
		)
		if err := row.Scan(&keep); err != nil {
			return err
		}
		result = cleanupClaim{Session: session, Claim: sessionClaim{Owner: owner, Fence: session.Revision}, KeepObject: keep}
		return nil
	})
	return result, err
}

func (r *postgresSessionRepository) DeleteClaimed(ctx context.Context, id string, claim sessionClaim) error {
	tag, err := r.db.DB().Exec(ctx, "tus_session_delete_claimed", `delete from upload_sessions
where id = $1::uuid and status = 'cleaning' and lease_owner = $2::uuid
  and revision = $3 and lease_until > clock_timestamp()`, id, claim.Owner, claim.Fence)
	if err != nil {
		return err
	}
	if tag.RowsAffected() != 1 {
		return ErrFenceLost
	}
	return nil
}
