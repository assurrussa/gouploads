# Changelog

## Unreleased

### Added

- Read-only `host.DiagnoseFile` and `FileRepo.GetFileLifecycle` provide a
  path-free snapshot by FileID, including retained finalization/deletion evidence,
  explicit unknown/unavailable states and sanitized source failures. Job lookup
  remains unavailable; authorization and any recovery remain host-owned.

- Opt-in MP3/WAV original uploads with stable `host.FileTypeAudio` (numeric
  value 7), canonical audio MIME metadata, deterministic final extensions and
  audio repository filters. Existing default policies and limits are unchanged.
- Shared byte sniffing recognizes tagged and untagged MP3 and normalizes WAV
  aliases consistently across reader, multipart, stored/TUS and finalization.
  Incomplete TUS signatures remain private until recognized. Audio in
  `media_resizer` mode fails before persistence; no resize job is scheduled.

### Fixed

- S3 multi-object deletion now includes the legacy Content-MD5 header required by
  older compatible providers while preserving the SDK CRC32 checksum and signing.
  The exact source-built integration service pin and regression contract are
  documented in `docs/s3-compatibility.md`; production providers are not upgraded.

- File image/video predicates no longer panic on short MIME strings.
- Generic TUS creation preflights the built-in uploader's effective original-only
  policy before allocating storage. Client MIME metadata remains optional;
  completion and final content/scanner checks remain in place. Custom uploader
  adapters can forward `ValidateUploadConfig(*host.FileUploadConfig) error`.
  Media-mode and CMS quarantine policy behavior is unchanged.

- Media admission now persists job-ID polling continuations, reconciles missed
  success callbacks and terminal failures, honors transient Retry-After, and
  keeps uncertain outcomes out of the terminal-failed state. Finalized rows
  with cleared uploader metadata cannot be downgraded by late failure notices.
  Upgrade all workers together; see `docs/media-admission.md`.

- Video previews and thumbnails are downloaded from their original video
  processor's origin when image and video resizers use different addresses.
  Their image media type, strict origin checks and credential-free downloads
  are preserved.

- S3 storage `Commit` preserves an existing object when the source and
  destination resolve to the same bucket and key, matching local storage.

## v0.10.0 - 2026-09-30

### Added

- Net/http-compatible upload adapter: `host.NewStandardUploadHandler` and
  `host.StandardUploadHandlerConfig` for mounting upload routes into standard Go
  HTTP routers (net/http, Chi, Gin, Echo) with configurable body limit,
  `host.DefaultBodyLimit` (32 MiB), and fail-fast `host.ErrIncompatibleBodyLimit`
  validation when the effective body limit is smaller than the underlying TUS
  store's chunk size. Missing or uninitialized upload handlers return
  `host.ErrNilUploadHandler`; multiple optional configurations return
  `host.ErrInvalidStandardHandlerConfig`.
- Standard `log/slog` logger adapter: `host.NewSlogLogger` wrapping standard Go
  `*slog.Logger` as a `gologger.Logger` for `OriginalRuntimeDeps`.
- Standalone PostgreSQL + MinIO quickstart (`examples/standalone-quickstart`)
  with Docker Compose, auto-migrations, transactional outbox finalization,
  async task polling, and tus-js drag & drop Web UI.

- Explicit `host.NewUploadHandlerWithPolicy` object authorization and actor
  mapping, optional `OriginalRuntimeDeps.ContentScanner`, partial-batch
  `host.BatchError`, and durable TUS `ReaderRequest.FinalizationKey`.
- Embedded lifecycle migration for finalization tombstones, retryable deletion
  plans, fenced S3 cleanup and active-primary uniqueness. Stop legacy workers
  for the documented coordinated transition; duplicate primary files must be
  resolved before migration.

- Stable `host.NewOriginalRuntime` wiring for original uploads without site,
  media-resizer, callback routes, a Redis client or a WebSocket server at runtime.
- `StorageConfig.ProcessingMode` (`STORAGE_PROCESSING_MODE`): the standalone
  constructor and configured DI default to `original_only`; `media_resizer`
  is explicit.
- A `finalize_original_file` outbox job that reads a persisted staging key,
  reuses artifact validation/checksum/finalization, and commits file metadata,
  after-jobs and cleanup under a row lock.
- Error-returning `host.BuildOutboxJobs` with original, media or mixed
  registration for draining old queues.
- Mode, payload, source confinement, ingress routing, retry, failure,
  concurrency and public-surface regression tests.

### Changed

- The dependency graph no longer includes `goshared`, `goredis` or
  `gowebsocket`. `host.UserID` and `host.EventID` are library-owned UUID types;
  text/JSON and SQL formats are unchanged, but hosts must convert former shared
  Go types at their boundary.
- Live notifications use the narrow `host.EventPublisher` contract. Hosts own
  transport adapters and lifecycle; `NewOriginalRuntime` accepts a nil publisher.
- HTTP hooks now preserve host `context.Context`: `StandardUploadHandler` propagates
  standard request context to `ContextBuilder`, `UploadResourceAuthorizer`,
  `ResolveActor`, `UploadStrategy.CanUpload`, `GetConfig`, `GetAfterJobs`, and
  `ReadPrefix`.
- TUS completion retains ready sessions until TTL for retries. MIME rejection
  stops PATCH writes; local uploads can accumulate a bounded private signature
  across small PATCH requests. Effective ingestion limits apply before TUS
  storage allocation and on resume.
- Presets that collide after canonicalization are rejected before IO. Deletion
  commits a hidden record and artifact plan before purging storage; uncertain
  finalization commits no longer delete final keys.
- Unknown named contexts are rejected, strategy policies are copied, pagination
  is bounded and `SetPrimary` verifies existing object ownership.

- Configured DI no longer implicitly selects media processing. Existing hosts
  must choose `media_resizer` explicitly and retain old workers/callbacks until
  in-flight jobs drain. Resizer/callback hostnames are no longer defaulted.
- Unsupported deep `uploadservice.New`/`Must` retain legacy behavior for
  compatibility. New hosts should use the stable constructor. `SkipResizer`
  retains its old meaning and is not a mode selector.

See `docs/standalone-uploads.md` for migration, asynchronous completion and
operational limitations. See `docs/standalone-verification.md` for the actual
checks and remaining release blockers; tests added is not tests passed.
