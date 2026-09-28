# gouploads

[![Go Reference](https://pkg.go.dev/badge/github.com/assurrussa/gouploads.svg)](https://pkg.go.dev/github.com/assurrussa/gouploads)
[![Go](https://github.com/assurrussa/gouploads/actions/workflows/go.yml/badge.svg)](https://github.com/assurrussa/gouploads/actions/workflows/go.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`gouploads` is an embedded upload and storage orchestration library for Go
applications. It supports multipart uploads, reader uploads, TUS, local/S3
storage, durable finalization, cleanup and opt-in media processing.

## Start without site or media-resizer

The standalone constructor is `host.NewOriginalRuntime`. Its default mode,
`original_only`, saves the original without a resizer service or a callback to
another application. Configured DI uses the same default. The application still
supplies PostgreSQL, a compatible transaction manager, a durable outbox worker
and persistent file storage; route mounting and authorization remain host-owned.

```go
runtime, err := host.NewOriginalRuntime(host.StorageConfig{
    Driver: host.StorageDriverLocal,
    Local: host.StorageLocalConfig{Root: "./var/files"},
}, host.OriginalRuntimeDeps{
    Database: database,
    Transaction: transactionManager,
    Outbox: outboxService,
    Logger: logger,
})
if err != nil {
    return err
}
// Register runtime.Jobs with the outbox worker before accepting uploads.
// Use runtime.Uploader, runtime.Files and runtime.TusStore with
// host.NewUploadHandler and host.NewFiberUploadHandler.
```

This is an embedding snippet, not a complete executable. See
[Standalone uploads](docs/standalone-uploads.md) for wiring, required migrations,
asynchronous completion, file types, safety limits and migration instructions.
The constructor does not start a worker or serve a directory automatically.
Do not expose temporary files or the entire storage root.

An upload returns a queued record. `finalize_original_file` reads the staged
object from storage, validates content/size, calculates SHA-256, writes the final
artifact and commits metadata and cleanup tasks. It never calls a resizer.
The single `main` artifact retains the original bytes. Completion is not
moderation approval or a guarantee that untrusted content is safe to publish.

## Enable media processing explicitly

Set `StorageConfig.ProcessingMode` to `host.ProcessingMediaResizer` (or map
`STORAGE_PROCESSING_MODE=media_resizer`) and wire the existing media commands,
resizer endpoints/tokens and independently authenticated callback route.
Configured presets or watermarking without enabled processing are rejected.
`SkipResizer` retains its old media-payload meaning; it is not a mode selector.

`host.BuildOutboxJobs` supports original commands, a complete media pipeline, or
both while draining old jobs. Deploy handlers before new producers, and keep
old media callbacks and workers until their queues drain. Unsupported deep
`uploadservice.New`/`Must` retain their historical media behavior for compatibility;
new applications should use the stable host facade.

## Supported packages and installation

The stable runtime package is `github.com/assurrussa/gouploads/host`.
External tests may use `github.com/assurrussa/gouploads/hosttest`.
`reference/externalconsumer` defines the supported package list. Deep
`domain/files/*`, `config`, `di`, `infrastructure/*` and `shared/*` packages are
implementation details, not an additional stable SDK.

```sh
go get github.com/assurrussa/gouploads@<published-version>
```

Use a known accessible version. This change removes runtime coupling to site
and media-resizer, **not** the remaining private Go-module dependencies. It is
not evidence that the repository or its dependency graph is anonymously public.

## Storage and TUS contracts

`host.NewStorage(cfg)` supports local storage and explicit generic S3 endpoints.
MinIO, Yandex Object Storage and Selectel use the same endpoint, region, bucket,
credentials and path-style fields; there is no hidden provider preset.
`host.Storage.DeleteBatch` supports idempotent deletion of approved artifact sets.

Local TUS is the existing filesystem-backed single-node profile. S3 TUS uses
PostgreSQL-backed multipart session state, fencing, crash reconciliation and
idempotent protocol finalization; Redis is not its source of truth. Hosts run
`host.MigrationFiles` for files/upload_sessions and their outbox backend's
separate migrations. `host.NewTusStore(cfg, database)` constructs the store.

S3 staging uses `staging/v1/tus/<session-id>/source.<ext>` and
`Cache-Control: private,no-store`. The adapter sends no object ACLs; staging
privacy and public delivery are bucket-policy responsibilities. Final artifacts
use deterministic keys below `media/v1/<object-type>/<object-id>/<file-slug>`.
Managed records keep relative keys and checksums; delivery URLs are composed at
HTTP/event boundaries. The original worker streams from `Storage.Open` and uses
the same content validation and artifact-writing path as processed media.

Replacement cleanup remains ownership-bound to `(ObjectType, ObjectID)`.
A staging-only deletion job has `FileID=0`; the prior file record and its
main/original/preset keys are not scheduled for deletion before successful new
finalization. Retry and uncertain-commit limitations are documented in the
standalone guide; permanently failed uploads still need safe reconciliation.

`FiberUploadHandler.RegisterCMSTusRoutes` mounts only `OPTIONS/POST/HEAD/PATCH`.
The isolated CMS quarantine lifecycle and `host.NewQuarantinePromoter` are
unchanged. Generic completion/read/delete routes must not be inherited by CMS
transports. `UploadRouteGuards` separates read, create/resume and delete policy.

## External media and storage probes

For explicit external processing, `host.NewSourceURLResolver(cfg)` creates a
fresh S3 SigV4 GetObject URL at dispatch time. It uses the configured staging
bucket and endpoint; no site source proxy is required. Signed queries must not
be logged. Local `StorageLocalConfig.SourceBaseURL` remains an optional internal
origin for confined temporary source paths. That setting is unnecessary for
original-only mode.

`host.NewStorageContractChecker(cfg)` is an explicit live storage probe for host
CLIs, not a startup side effect. It checks staging privacy, presigned reads,
public HEAD/GET/Range behavior, transport/cache metadata, deletion and cleanup.

## Development and release

Use the declared Go/toolchain and repository-local Makefile caches:

```sh
make check
make externalconsumer-local
make publish-readiness VERSION=<planned-version>
make release-readiness VERSION=<published-version>
make externalconsumer-published VERSION=<published-version>
```

The standalone candidate's actual validation and outstanding gates are recorded
in [Verification](docs/standalone-verification.md). Do not treat a local replace
probe as a public release check. See [RELEASING.md](RELEASING.md) for the existing
release process and [CHANGELOG.md](CHANGELOG.md) for the default-mode change.

[Host integration](docs/host-integration.md) describes the existing detailed
media pipeline; use the standalone guide first for the new default.
[Project map](docs/project-map.md) describes the package boundaries.

## License

MIT
