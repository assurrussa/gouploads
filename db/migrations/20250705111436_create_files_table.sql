-- +goose Up
-- +goose StatementBegin
create table if not exists files
(
    id                serial primary key,
    user_id           integer      null     default null,
    manager_id        integer      null     default null,
    object_type       varchar(255),
    object_id         integer,
    original_filename VARCHAR(255) NOT NULL,
    filename          VARCHAR(255) NOT NULL,
    is_primary        boolean      not null default false,
    folder_path       VARCHAR(255) NOT NULL DEFAULT '',
    provider          VARCHAR(50)  NULL     DEFAULT NULL,
    size              BIGINT       NOT NULL DEFAULT 0,
    mime_type         VARCHAR(100) NOT NULL DEFAULT '',
    moderate          smallint              default 0,
    block_cause       varchar(255),
    name              VARCHAR(255) NOT NULL DEFAULT '',
    description       varchar(255) null     default null,
    file_type         smallint              default 0,
    position          integer               default 0,
    url               varchar(500) null     default null,
    slug              varchar(255) not null,
    locale            varchar(255) null     default null,
    data              jsonb,
    created_at        TIMESTAMP    NOT NULL DEFAULT NOW(),
    updated_at        TIMESTAMP    NOT NULL DEFAULT NOW(),
    deleted_at        TIMESTAMP,
    published_at      TIMESTAMP
);

-- Создаем индексы для оптимизации запросов
CREATE UNIQUE INDEX files_slug_idx ON files (slug);
create index files_documents_user_idx on files (user_id);
CREATE INDEX files_documents_manager_idx ON files (manager_id);
CREATE INDEX idx_files_created_at ON files (created_at);
CREATE INDEX idx_files_deleted_at ON files (deleted_at);
CREATE INDEX idx_files_mime_type ON files (mime_type);
CREATE INDEX idx_files_file_type ON files (file_type);
create index files_documents_idx on files (object_type, object_id);
-- CREATE UNIQUE INDEX IF NOT EXISTS files_primary_unique_idx
--     ON files (object_type, object_id)
--     WHERE is_primary AND object_id IS NOT NULL AND deleted_at IS NULL;

-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
drop table files;
-- +goose StatementEnd
