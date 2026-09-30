-- +goose Up
-- +goose StatementBegin
CREATE TABLE upload_finalizations (
    finalization_key uuid PRIMARY KEY,
    binding_hash varchar(64) NOT NULL CHECK (length(binding_hash) = 64),
    file_id bigint NOT NULL CHECK (file_id > 0),
    created_at timestamptz NOT NULL DEFAULT clock_timestamp()
);
-- Deliberately no FK: an idempotency tombstone must survive file removal.
CREATE INDEX upload_finalizations_file_idx ON upload_finalizations (file_id);

CREATE TABLE file_deletions (
    file_id bigint PRIMARY KEY,
    payload jsonb NOT NULL,
    created_at timestamptz NOT NULL DEFAULT clock_timestamp(),
    completed_at timestamptz
);
CREATE INDEX file_deletions_pending_idx ON file_deletions (created_at, file_id)
    WHERE completed_at IS NULL;

ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_status_check;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_status_check
    CHECK (status IN ('active', 'finalizing', 'ready', 'cleaning'));
CREATE INDEX upload_sessions_expiry_all_idx ON upload_sessions (expires_at, id);

UPDATE files SET file_type = CASE
    WHEN mime_type LIKE 'image/%' THEN 1
    WHEN mime_type LIKE 'video/%' THEN 2
    WHEN mime_type = 'application/pdf' THEN 3
    WHEN mime_type = 'application/vnd.openxmlformats-officedocument.wordprocessingml.document' THEN 4
    WHEN mime_type LIKE 'text/%' THEN 6
    ELSE file_type END
WHERE file_type = 0;

-- Never silently choose a primary file on behalf of a host application.
DO $$
BEGIN
    IF EXISTS (
        SELECT 1 FROM files
        WHERE is_primary AND object_id IS NOT NULL AND deleted_at IS NULL
        GROUP BY object_type, object_id HAVING count(*) > 1
    ) THEN
        RAISE EXCEPTION 'gouploads: duplicate primary files; resolve them before applying lifecycle safety migration';
    END IF;
END $$;
CREATE UNIQUE INDEX files_primary_unique_idx ON files (object_type, object_id)
    WHERE is_primary AND object_id IS NOT NULL AND deleted_at IS NULL;
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DO $$
BEGIN
    IF EXISTS (SELECT 1 FROM upload_sessions WHERE status = 'cleaning') OR
       EXISTS (SELECT 1 FROM file_deletions WHERE completed_at IS NULL) THEN
        RAISE EXCEPTION 'gouploads: drain pending cleanup/deletion operations before rollback';
    END IF;
END $$;
DROP INDEX files_primary_unique_idx;
DROP INDEX upload_sessions_expiry_all_idx;
ALTER TABLE upload_sessions DROP CONSTRAINT upload_sessions_status_check;
ALTER TABLE upload_sessions ADD CONSTRAINT upload_sessions_status_check
    CHECK (status IN ('active', 'finalizing', 'ready'));
DROP TABLE file_deletions;
DROP TABLE upload_finalizations;
-- Corrected file_type values are intentionally not reverted.
-- +goose StatementEnd
