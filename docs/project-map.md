# Project Map

Verified against the local repository on 2026-07-10.

## Purpose

`gouploads` is a reusable Go module for upload/storage orchestration. It gives
host applications a stable embedding facade for file uploads, TUS resumable
uploads, local and S3/Ceph storage, media-processing callbacks, cleanup tasks,
and upload-related outbox jobs.

The module path is `github.com/assurrussa/gouploads`; `go.mod` currently uses
Go `1.26`.

## Stable Consumer Surface

The supported external package list lives in `reference/externalconsumer`.
Today it contains only:

- `github.com/assurrussa/gouploads/host` for runtime embedding.
- `github.com/assurrussa/gouploads/hosttest` for external consumer tests.

Runtime host code should only import `host`. Tests and test helper packages may
import `hosttest`. Deep packages remain implementation details unless the
external consumer manifest and release docs explicitly promote them.

## Package Map

- `host`: public runtime facade. It aliases stable config, file model, upload,
  TUS, migration, cleanup, resize callback, URL, DI bootstrap, repository, and
  outbox job contracts.
- `hosttest`: public test-support facade. Normal builds expose stable matcher
  and storage aliases; integration builds expose PostgreSQL test helpers behind
  the `integration` build tag.
- `reference/externalconsumer`: machine-readable public package manifest and
  tests that keep blank imports in sync with the manifest.
- `cmd/importpolicy`: scanner for host repositories. It rejects deep
  `gouploads` imports outside the stable host/test surfaces.
- `cmd/externalconsumerprobe`: generated clean-consumer compile probe for local
  checkouts or published versions.
- `domain/files`: internal upload domain. It contains models, repositories,
  services, HTTP transport, use cases, outbox jobs, shared events, TUS store
  contracts, and internal test helpers.
- `infrastructure/storage/files`: internal local and Ceph/S3 storage adapters.
- `config`: internal storage, TUS, image pipeline, and video pipeline config
  structs re-exported narrowly through `host`.
- `di`: internal dependency module bootstrap re-exported through
  `host.BootstrapDependencies()`.
- `db/migrations` and `host/migrations`: `files` and durable
  `upload_sessions` SQL. `host/migrations` is embedded and exposed through
  `host.MigrationsFS()` and `host.MigrationFiles()`.
- `tools/toolsmocks`: repository-local `go generate` helper over `mockgen`.

## Runtime Flow

1. A host maps local env/config into `host.StorageConfig`.
2. The host runs gouploads migrations and wires database, transaction manager,
   logger, TUS store, file repository, upload use cases, and optional
   `host.BootstrapDependencies()`. Redis is not TUS durable state.
3. The host creates `host.NewUploadHandler(...)` with an auth/session-aware
   `ContextBuilder` and URL composer.
4. The host registers `host.UploadStrategy` implementations for contexts such
   as `default`, `avatar`, `rich-text`, `gallery`, or `video`.
5. `host.NewFiberUploadHandler(handler).RegisterGroupRoutes(...)` mounts upload
   routes into the host Fiber app.
6. Upload use cases persist file metadata, write storage objects, and enqueue
   upload, resize, listen-resize, or delete jobs through the host outbox
   service.
7. The external `media-resizer` service receives image/video jobs and calls the
   host callback route.
8. The host callback parses the request into `host.BuildListenResizeRequest(...)`
   and calls the configured listen-resize handler.
9. Scheduled cleanup can use `host.NewCleanFilesUseCase` and
   `host.NewCleanTusUseCase`.

## Host Responsibilities

The host application owns:

- env/config loading and validation around `host.StorageConfig`;
- auth/session extraction into `host.UploadContext`;
- route mounting and middleware policy;
- object-type policy and upload strategies;
- database, Redis, transaction manager, logger, and event stream wiring;
- outbox backend selection, migrations, lifecycle, and worker process;
- media-resizer deployment URL, API token, and callback URL;
- migration execution order for `gouploads` files and selected outbox backend.

`gouploads` owns the file upload contracts, file table migration, storage
adapter contracts, TUS contracts, media callback request helpers, and upload
outbox job factories. It does not own host auth, tenant policy, deployment
topology, or outbox backend migrations.

## Main Config Keys

Storage driver and URL:

- `APP_DOMAIN_URL`
- `STORAGE_DRIVER`
- `STORAGE_LOCAL_ROOT`
- `STORAGE_LOCAL_BASE_URL`

S3/Ceph:

- `STORAGE_S3_ENDPOINT`
- `STORAGE_S3_HOST`
- `STORAGE_S3_SOURCE_HOST`
- `STORAGE_S3_SOURCE_URL_TTL` (default `6h`, maximum `168h`)
- `STORAGE_S3_ACL`
- `STORAGE_S3_REGION`
- `STORAGE_S3_BUCKET`
- `STORAGE_S3_ACCESS_KEY`
- `STORAGE_S3_SECRET_KEY`
- `STORAGE_S3_SESSION_TOKEN`
- `STORAGE_S3_FORCE_PATH_STYLE`
- `STORAGE_S3_TRANSFORM_HOST`
- `STORAGE_S3_DISABLE_SSL`
- `STORAGE_S3_TIMEOUT`
- `STORAGE_S3_MAX_RETRIES`

TUS:

- `STORAGE_TUS_PART_SIZE`
- `STORAGE_TUS_SESSION_TTL`
- `STORAGE_TUS_LEASE_TTL`
- `STORAGE_TUS_QUARANTINE_PREFIX`
- `STORAGE_TUS_CLEANUP_INTERVAL`
- `STORAGE_TUS_CLEANUP_SPEC`

Image/video pipelines:

- `STORAGE_IMAGE_DEFAULT_FORMAT`
- `STORAGE_IMAGE_RESIZER_HOST`
- `STORAGE_IMAGE_RESIZER_WEBHOOK_HOST`
- `STORAGE_IMAGE_RESIZER_TOKEN`
- `STORAGE_VIDEO_RESIZER_HOST`
- `STORAGE_VIDEO_RESIZER_WEBHOOK_HOST`
- `STORAGE_VIDEO_RESIZER_TOKEN`

Preset arrays and watermark settings are TOML-structured config, not simple
single env values.

## Verification Gates

- `make release-readiness`: primary reusable-boundary gate for local readiness.
- `make check`: broader local gate, including generation, formatting, lint,
  race tests, and coverage HTML.
- `make test-surface`: public facade and probe packages.
- `make test-surface-integration`: `hosttest` integration build-tag surface.
- `make test-tus-postgres-integration`: real PostgreSQL fencing and
  cross-repository session-state gate.
- `make externalconsumer-local`: generated clean consumer using local
  `replace`.
- `make externalconsumer-published VERSION=vX.Y.Z`: generated clean consumer
  resolving a published tag without local `replace`.
- `go run ./cmd/importpolicy --repo-root .. --consumers site/backend,goadmin,site/fixtures/second-go-host`:
  strict sibling host import boundary check.

The Makefile sets repository-local `.go-cache` locations. Direct `go` commands
in sandboxed environments should set `GOCACHE` and `GOPATH` under this repo.

## Cross-Project Context

Shared wiki pages currently describe `gouploads`, `media-resizer`, and `outbox`
as linked platforms. Local docs/code still win for exact command names, package
paths, config keys, and release gates.

If this repository changes a public package, config contract, media callback,
outbox integration, migration contract, or reusable-boundary gate, update the
matching shared wiki page after verifying the local behavior.
