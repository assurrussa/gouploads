# Changelog

## Unreleased

### Added

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
  is explicit. Private compile-time dependencies have not been removed.
- A `finalize_original_file` outbox job that reads a persisted staging key,
  reuses artifact validation/checksum/finalization, and commits file metadata,
  after-jobs and cleanup under a row lock.
- Error-returning `host.BuildOutboxJobs` with original, media or mixed
  registration for draining old queues.
- Mode, payload, source confinement, ingress routing, retry, failure,
  concurrency and public-surface regression tests.

### Changed

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
