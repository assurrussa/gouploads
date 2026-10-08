-- +goose Up
-- +goose StatementBegin
create table file_job_associations (
    job_id uuid primary key,
    file_id bigint not null check (file_id > 0),
    generation text not null check (generation <> ''),
    object_type text not null,
    object_id bigint null,
    operation text not null check (operation in (
        'original_finalization', 'media_admission', 'media_finalization', 'deletion')),
    job_name text not null check (job_name <> ''),
    schema_version integer not null check (schema_version > 0),
    created_at timestamptz not null default now()
);
create index file_job_associations_lookup
    on file_job_associations (file_id, operation, created_at, job_id);
-- No foreign keys: queue ack and file deletion must retain these links.
-- No backfill: old jobs cannot be attributed by guessed IDs or payload scanning.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table file_job_associations;
-- +goose StatementEnd
