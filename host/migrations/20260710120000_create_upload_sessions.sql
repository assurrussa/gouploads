-- +goose Up
-- +goose StatementBegin
create table if not exists upload_sessions
(
    id                   uuid primary key,
    upload_length        bigint not null check (upload_length >= 0),
    upload_offset        bigint not null default 0 check (upload_offset >= 0),
    metadata             jsonb not null default '{}'::jsonb,
    object_path          text not null unique,
    original_name        text not null,
    file_name            text not null,
    mime_type            text not null default '',
    owner_id             bigint not null default 0,
    owner_uuid           uuid not null,
    multipart_upload_id  text not null,
    parts                jsonb not null default '{}'::jsonb,
    status               text not null default 'active'
        check (status in ('active', 'finalizing', 'ready')),
    revision             bigint not null default 0,
    lease_owner          uuid,
    lease_until          timestamptz,
    finalization_key     uuid not null unique,
    expires_at           timestamptz not null,
    finalized_at         timestamptz,
    created_at           timestamptz not null default clock_timestamp(),
    updated_at           timestamptz not null default clock_timestamp(),
    check (upload_offset <= upload_length),
    check ((lease_owner is null) = (lease_until is null))
);

create index if not exists upload_sessions_expiry_idx
    on upload_sessions (expires_at, updated_at)
    where status <> 'ready';
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table if exists upload_sessions;
-- +goose StatementEnd
