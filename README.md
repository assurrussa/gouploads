# gouploads

[![Go Reference](https://pkg.go.dev/badge/github.com/assurrussa/gouploads.svg)](https://pkg.go.dev/github.com/assurrussa/gouploads)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](LICENSE)

`gouploads` is an embedded upload and storage orchestration library for Go
applications. It supports multipart uploads, reader uploads, resumable TUS, local/S3
storage, durable finalization, safe cleanup and opt-in media processing.

## Highlights

- **Zero-Redis TUS Fencing**: PostgreSQL transactional advisory locks (`pg_advisory_xact_lock`) and monotonic revisions guarantee atomic chunk offset validation and lease fencing without race conditions or Redis dependency.
- **Transactional Outbox Integrity**: Upload metadata, background staging-to-final processing jobs, and cleanup plans are recorded within a single ACID transaction.
- **Standard `net/http` Adapter**: Mount upload routes into **net/http or Chi**, or via standard handler adapters in **Gin/Echo**, using `host.NewStandardUploadHandler`, alongside native Fiber v3 support (`host.NewFiberUploadHandler`).
- **Standard `log/slog`**: Seamlessly wrap standard Go `*slog.Logger` with `host.NewSlogLogger(slog.Default())` or use `gologger`.
- **Ready in 2 Minutes**: Try the runnable [Standalone Quickstart](examples/standalone-quickstart) with Docker Compose (PostgreSQL + MinIO + tus-js Web UI).

## Start without site or media-resizer

The standalone constructor is `host.NewOriginalRuntime`. Its default mode,
`original_only`, saves the original without a resizer service or a callback to
another application. Configured DI uses the same default. The application still
supplies PostgreSQL, a compatible transaction manager, a durable outbox worker
and persistent file storage; route mounting and authorization remain host-owned.

For local-driver runtimes, optional `OriginalRuntimeDeps.Storage` accepts
caller-owned storage wrappers. This addition is merged but unreleased; it is not
included in `v0.12.0`. See [storage injection and instrumentation](docs/standalone-uploads.md#caller-owned-storage)
for TUS handoff, ownership and compatibility limits.

```go
runtime, err := host.NewOriginalRuntime(host.StorageConfig{
    Driver: host.StorageDriverLocal,
    Local:  host.StorageLocalConfig{Root: "./var/files"},
}, host.OriginalRuntimeDeps{
    Database:    database,
    Transaction: transactionManager,
    Outbox:      outboxService,
    Logger:      host.NewSlogLogger(slog.Default()), // or gologger
})
if err != nil {
    return err
}
// 1. Register runtime.Jobs with your outbox worker before accepting uploads.
// 2. Mount with standard net/http (or Chi):
// Gin/Echo use their standard http.Handler adapters.
stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
if err != nil {
    return err
}
http.Handle("/files/", stdHandler)

// Or mount with Fiber v3:
fiberHandler := host.NewFiberUploadHandler(uploadHandler)
fiberHandler.RegisterGroupRoutes("/files", fiberApp)
```

This is an embedding snippet, not a complete executable. See
[Standalone uploads](docs/standalone-uploads.md) for wiring, required migrations,
asynchronous completion, file types, safety limits and migration instructions.
The constructor does not start a worker or serve a directory automatically.
Do not expose temporary files or the entire storage root.

`NewStandardUploadHandler` requires an initialized `UploadHandler` and accepts
at most one optional `StandardUploadHandlerConfig`. Invalid inputs return
`ErrNilUploadHandler` or `ErrInvalidStandardHandlerConfig`. Its default body limit
is `DefaultBodyLimit` (32 MiB); for larger TUS chunks, configure
`StandardUploadHandlerConfig.BodyLimit` explicitly. A body limit below the TUS
chunk size returns `ErrIncompatibleBodyLimit` during construction.
The body limit covers the entire HTTP request body. For multipart uploads, allow
framing and part-header overhead beyond the strategy's `MaxFileSize`.

An upload returns a queued record. `finalize_original_file` reads the staged
object from storage, validates content/size, calculates SHA-256, writes the final
artifact and commits metadata and cleanup tasks. It never calls a resizer.
The single `main` artifact retains the original bytes. Completion is not
moderation approval or a guarantee that untrusted content is safe to publish.

## Inspect file lifecycle safely

The diagnostics and inventory APIs below are merged but unreleased; they are not
included in `v0.12.0`.

`host.DiagnoseFile` reads a path-free lifecycle snapshot by FileID through
`host.FileRepo`, reporting file, upload, finalization handoff and deletion states.
The lifecycle-only result keeps job state unavailable. Opt-in
[PostgreSQL job evidence](docs/file-job-evidence.md) adds durable producer links
and separate read-only `host.InspectFileJobs` diagnostics with partial coverage;
missing queue history never means success. Hosts authorize the FileID before calling; the API installs no route and
performs no recovery or deletion. `host.InspectDeletionRetention` adds a read-only
aggregate inventory of retained deletion plans, with caller-supplied age what-ifs
and JSON-text byte estimates. See [the inventory contract](docs/deletion-retention-inventory.md)
and [diagnostics and recovery roadmap](docs/file-diagnostics.md).

## Opt in to MP3/WAV originals

MP3 and WAV use `host.FileTypeAudio` (7) and the same original storage/outbox
workflow. Register a host upload strategy with explicit `.mp3`/`.wav` extension
and `audio/mpeg`/`audio/wav` MIME allowlists and a bounded `MaxFileSize`. The
generic and rich-text defaults do not enable audio or increase their limits.
WAV aliases (`audio/wave`, `audio/x-wav`, `audio/vnd.wave`) normalize to
`audio/wav`; common MP3 aliases normalize to `audio/mpeg`. Type detection uses
bytes, including untagged MPEG Layer III frames, rather than a supplied MIME.

Audio requires `original_only`; `media_resizer`, even with `SkipResizer`,
rejects audio without scheduling unsupported image/video processing. This
capability preserves the original; it does not transcode or validate complete
audio decoding. See [host audio policy](docs/host-integration.md#opt-in-audio-originals).

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
go get github.com/assurrussa/gouploads@v0.12.0
```

This installs the latest published release, `v0.12.0`. Use its
[version-matched README](https://github.com/assurrussa/gouploads/blob/v0.12.0/README.md)
and [standalone guide](https://github.com/assurrussa/gouploads/blob/v0.12.0/docs/standalone-uploads.md)
when integrating that release. The current source documentation also describes
the explicitly marked, merged-but-unreleased additions above; installing
`v0.12.0` does not provide them.

The public module resolves through the Go module proxy and checksum database.
Installation does not require GitHub authentication or a
`GOPRIVATE` setting. The dependency graph no longer includes
`goshared`, `goredis` or `gowebsocket`. Runtime notifications use the narrow
`host.EventPublisher` contract; the host owns any WebSocket adapter and lifecycle.
See [anonymous consumer checks](docs/anonymous-consumer.md) for isolated source
and published-tag probes. Release readiness requires the actual published tag
to pass both ordinary and integration-tag consumer builds without replacements.

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

Use `host.NewUploadHandlerWithPolicy` to authorize operations on the actual
object. Its zero policy denies access; `TrustRouteGuards` is an explicit host
responsibility. The legacy `host.NewUploadHandler` trusts external route guards:
authentication alone is insufficient. Hosts using it must enforce object-level
read, upload/resume and delete authorization. Prefer the explicit policy
constructor for new integrations; the legacy default is unchanged.

`OriginalRuntimeDeps.ContentScanner` optionally scans the same
private bytes subsequently published. TUS completion carries a durable
`ReaderRequest.FinalizationKey` and retains ready sessions until TTL for retries.
Batch failures return the accepted prefix and `*host.BatchError` (HTTP 207).
See [lifecycle hardening](docs/review-hardening.md) for required migrations,
worker transition, public policy/scanner contracts and limitations.

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

Use the declared Go/toolchain and the shared Go caches configured by the Makefile:

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

Media-resizer admission and durable client reconciliation follow the
[media admission contract](docs/media-admission.md). A 202 is not completion;
all client workers must be upgraded together before continuation-producing
clients are enabled.
