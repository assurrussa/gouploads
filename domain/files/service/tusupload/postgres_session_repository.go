package tusupload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/jackc/pgx/v5"
)

const sessionColumns = `
id::text,
upload_length,
upload_offset,
metadata,
object_path,
original_name,
file_name,
mime_type,
owner_id,
owner_uuid,
multipart_upload_id,
parts,
status,
revision,
finalization_key::text,
expires_at,
created_at,
updated_at`

type postgresSessionRepository struct {
	db pgsql.Client
}

func newPostgresSessionRepository(db pgsql.Client) (*postgresSessionRepository, error) {
	if db == nil {
		return nil, errors.New("tus postgres repository: database is required")
	}

	return &postgresSessionRepository{db: db}, nil
}

func (r *postgresSessionRepository) Create(ctx context.Context, session s3Session) error {
	metadata, err := json.Marshal(session.Metadata)
	if err != nil {
		return fmt.Errorf("marshal tus metadata: %w", err)
	}
	parts, err := json.Marshal(session.Parts)
	if err != nil {
		return fmt.Errorf("marshal tus parts: %w", err)
	}

	const query = `
insert into upload_sessions (
    id, upload_length, upload_offset, metadata, object_path, original_name,
    file_name, mime_type, owner_id, owner_uuid, multipart_upload_id, parts,
    status, revision, finalization_key, expires_at, created_at, updated_at
) values (
    $1::uuid, $2, $3, $4::jsonb, $5, $6,
    $7, $8, $9, $10::uuid, $11, $12::jsonb,
    $13, $14, $15::uuid, $16, $17, $18
)`
	_, err = r.db.DB().Exec(
		ctx,
		"tus_session_create",
		query,
		session.ID,
		session.UploadLength,
		session.Offset,
		metadata,
		session.Path,
		session.OriginalName,
		session.FileName,
		session.MimeType,
		session.OwnerID,
		session.OwnerUUID.String(),
		session.UploadID,
		parts,
		session.Status,
		session.Revision,
		session.FinalizationKey,
		session.ExpiresAt,
		session.CreatedAt,
		session.UpdatedAt,
	)
	if err != nil {
		return fmt.Errorf("create tus session: %w", err)
	}

	return nil
}

func (r *postgresSessionRepository) Get(ctx context.Context, id string) (s3Session, error) {
	query := "select " + sessionColumns + " from upload_sessions where id = $1::uuid"
	return scanSession(r.db.DB().QueryRow(ctx, "tus_session_get", query, id))
}

func (r *postgresSessionRepository) ClaimAppend(
	ctx context.Context,
	id string,
	expectedOffset int64,
	owner string,
	leaseTTL time.Duration,
	sessionTTL time.Duration,
) (s3Session, sessionClaim, error) {
	query := `
update upload_sessions
set lease_owner = $3::uuid,
    lease_until = clock_timestamp() + ($4 * interval '1 millisecond'),
    revision = revision + 1,
    expires_at = greatest(expires_at, clock_timestamp() + ($5 * interval '1 millisecond')),
    updated_at = clock_timestamp()
where id = $1::uuid
  and upload_offset = $2
  and status = 'active'
  and (lease_until is null or lease_until <= clock_timestamp() or lease_owner = $3::uuid)
returning ` + sessionColumns

	session, err := scanSession(r.db.DB().QueryRow(
		ctx,
		"tus_session_claim_append",
		query,
		id,
		expectedOffset,
		owner,
		durationMilliseconds(leaseTTL),
		durationMilliseconds(sessionTTL),
	))
	if err == nil {
		return session, sessionClaim{Owner: owner, Fence: session.Revision}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return s3Session{}, sessionClaim{}, err
	}

	current, getErr := r.Get(ctx, id)
	if getErr != nil {
		return s3Session{}, sessionClaim{}, getErr
	}
	if current.Status == StatusReady {
		return s3Session{}, sessionClaim{}, ErrUploadFinalized
	}
	if current.Offset != expectedOffset {
		return current, sessionClaim{}, ErrOffsetMismatch
	}

	return current, sessionClaim{}, ErrUploadBusy
}

func (r *postgresSessionRepository) CommitPart(
	ctx context.Context,
	id string,
	claim sessionClaim,
	expectedOffset int64,
	part durablePart,
	mimeType string,
	sessionTTL time.Duration,
) (s3Session, error) {
	current, err := r.Get(ctx, id)
	if err != nil {
		return s3Session{}, err
	}
	current.Parts[part.Number] = part
	parts, err := json.Marshal(current.Parts)
	if err != nil {
		return s3Session{}, fmt.Errorf("marshal tus parts: %w", err)
	}

	query := `
update upload_sessions
set upload_offset = $5,
    parts = $6::jsonb,
    mime_type = case when mime_type = '' then $7 else mime_type end,
    lease_owner = null,
    lease_until = null,
    revision = revision + 1,
    expires_at = greatest(expires_at, clock_timestamp() + ($8 * interval '1 millisecond')),
    updated_at = clock_timestamp()
where id = $1::uuid
  and upload_offset = $2
  and lease_owner = $3::uuid
  and revision = $4
  and lease_until > clock_timestamp()
  and status = 'active'
returning ` + sessionColumns

	updated, err := scanSession(r.db.DB().QueryRow(
		ctx,
		"tus_session_commit_part",
		query,
		id,
		expectedOffset,
		claim.Owner,
		claim.Fence,
		expectedOffset+part.Size,
		parts,
		mimeType,
		durationMilliseconds(sessionTTL),
	))
	if errors.Is(err, ErrNotFound) {
		return s3Session{}, ErrFenceLost
	}
	return updated, err
}

func (r *postgresSessionRepository) ClaimFinalize(
	ctx context.Context,
	id string,
	owner string,
	leaseTTL time.Duration,
	sessionTTL time.Duration,
) (s3Session, sessionClaim, error) {
	query := `
update upload_sessions
set status = 'finalizing',
    lease_owner = $2::uuid,
    lease_until = clock_timestamp() + ($3 * interval '1 millisecond'),
    revision = revision + 1,
    expires_at = greatest(expires_at, clock_timestamp() + ($4 * interval '1 millisecond')),
    updated_at = clock_timestamp()
where id = $1::uuid
  and upload_offset = upload_length
  and status in ('active', 'finalizing')
  and (lease_until is null or lease_until <= clock_timestamp() or lease_owner = $2::uuid)
returning ` + sessionColumns

	session, err := scanSession(r.db.DB().QueryRow(
		ctx,
		"tus_session_claim_finalize",
		query,
		id,
		owner,
		durationMilliseconds(leaseTTL),
		durationMilliseconds(sessionTTL),
	))
	if err == nil {
		return session, sessionClaim{Owner: owner, Fence: session.Revision}, nil
	}
	if !errors.Is(err, ErrNotFound) {
		return s3Session{}, sessionClaim{}, err
	}

	current, getErr := r.Get(ctx, id)
	if getErr != nil {
		return s3Session{}, sessionClaim{}, getErr
	}
	if current.Status == StatusReady {
		return current, sessionClaim{}, ErrUploadFinalized
	}
	if current.Offset != current.UploadLength {
		return current, sessionClaim{}, ErrOffsetMismatch
	}

	return current, sessionClaim{}, ErrUploadBusy
}

func (r *postgresSessionRepository) CommitFinalize(
	ctx context.Context,
	id string,
	claim sessionClaim,
) (s3Session, error) {
	query := `
update upload_sessions
set status = 'ready',
    lease_owner = null,
    lease_until = null,
    revision = revision + 1,
    finalized_at = clock_timestamp(),
    updated_at = clock_timestamp()
where id = $1::uuid
  and lease_owner = $2::uuid
  and revision = $3
  and lease_until > clock_timestamp()
  and status = 'finalizing'
returning ` + sessionColumns

	session, err := scanSession(r.db.DB().QueryRow(
		ctx,
		"tus_session_commit_finalize",
		query,
		id,
		claim.Owner,
		claim.Fence,
	))
	if errors.Is(err, ErrNotFound) {
		return s3Session{}, ErrFenceLost
	}
	return session, err
}

func (r *postgresSessionRepository) Release(ctx context.Context, id string, claim sessionClaim) error {
	const query = `
update upload_sessions
set lease_owner = null,
    lease_until = null,
    revision = revision + 1,
    updated_at = clock_timestamp()
where id = $1::uuid and lease_owner = $2::uuid and revision = $3`
	_, err := r.db.DB().Exec(ctx, "tus_session_release", query, id, claim.Owner, claim.Fence)
	if err != nil {
		return fmt.Errorf("release tus session: %w", err)
	}
	return nil
}

func (r *postgresSessionRepository) Delete(ctx context.Context, id string) error {
	tag, err := r.db.DB().Exec(
		ctx,
		"tus_session_delete",
		"delete from upload_sessions where id = $1::uuid",
		id,
	)
	if err != nil {
		return fmt.Errorf("delete tus session: %w", err)
	}
	if tag.RowsAffected() == 0 {
		return ErrNotFound
	}
	return nil
}

func (r *postgresSessionRepository) ListExpired(
	ctx context.Context,
	before time.Time,
	limit int,
) ([]string, error) {
	const query = `
select id::text
from upload_sessions
where status <> 'ready'
  and expires_at < $1
  and (lease_until is null or lease_until <= clock_timestamp())
order by expires_at, id
limit $2`
	rows, err := r.db.DB().Query(ctx, "tus_session_list_expired", query, before, limit)
	if err != nil {
		return nil, fmt.Errorf("list expired tus sessions: %w", err)
	}
	defer rows.Close()

	ids := make([]string, 0, limit)
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, fmt.Errorf("scan expired tus session: %w", err)
		}
		ids = append(ids, id)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate expired tus sessions: %w", err)
	}
	return ids, nil
}

func (r *postgresSessionRepository) HasMultipart(
	ctx context.Context,
	objectPath string,
	uploadID string,
) (bool, error) {
	const query = `
select exists(
    select 1
    from upload_sessions
    where object_path = $1 and multipart_upload_id = $2 and status <> 'ready'
)`
	var exists bool
	if err := r.db.DB().QueryRow(
		ctx,
		"tus_session_has_multipart",
		query,
		objectPath,
		uploadID,
	).Scan(&exists); err != nil {
		return false, fmt.Errorf("check tus multipart session: %w", err)
	}
	return exists, nil
}

func scanSession(row pgx.Row) (s3Session, error) {
	var session s3Session
	var metadata []byte
	var parts []byte
	if err := row.Scan(
		&session.ID,
		&session.UploadLength,
		&session.Offset,
		&metadata,
		&session.Path,
		&session.OriginalName,
		&session.FileName,
		&session.MimeType,
		&session.OwnerID,
		&session.OwnerUUID,
		&session.UploadID,
		&parts,
		&session.Status,
		&session.Revision,
		&session.FinalizationKey,
		&session.ExpiresAt,
		&session.CreatedAt,
		&session.UpdatedAt,
	); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return s3Session{}, ErrNotFound
		}
		return s3Session{}, fmt.Errorf("scan tus session: %w", err)
	}
	if err := json.Unmarshal(metadata, &session.Metadata); err != nil {
		return s3Session{}, fmt.Errorf("unmarshal tus metadata: %w", err)
	}
	if err := json.Unmarshal(parts, &session.Parts); err != nil {
		return s3Session{}, fmt.Errorf("unmarshal tus parts: %w", err)
	}
	if session.Parts == nil {
		session.Parts = make(map[int32]durablePart)
	}
	session.Quarantined = true

	return session, nil
}

func durationMilliseconds(value time.Duration) int64 {
	if value <= 0 {
		return 1
	}
	return max(1, value.Milliseconds())
}
