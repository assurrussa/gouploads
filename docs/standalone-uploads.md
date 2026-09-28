# Standalone original uploads

Status: implementation candidate; see `standalone-verification.md` for actual
verification. This change is not a publication or a deployment.

## Default and explicit processing

The supported standalone constructor is `host.NewOriginalRuntime`. It and the
configured DI uploader interpret an empty `StorageConfig.ProcessingMode` as
`host.ProcessingOriginalOnly` (`original_only`). The host may map
`STORAGE_PROCESSING_MODE` into that field. Processing by `media-resizer` requires
an explicit `media_resizer` value and the existing media pipeline wiring.

The original pipeline requires neither the `site` application, a media-resizer
process, a callback HTTP route, a resizer token, nor an HTTP client to the source
application. It still requires a database, transaction manager, durable outbox
with a running worker, and persistent local or S3 storage. The host owns route
mounting, authentication, authorization, migrations and the outbox lifecycle.
Redis and a WebSocket server are not runtime requirements of the new constructor.
Private Go modules are still present in the build graph; removing them is a
separate migration, not accomplished by disabling a runtime feature.

```go
runtime, err := host.NewOriginalRuntime(host.StorageConfig{
    Driver: host.StorageDriverLocal,
    Local: host.StorageLocalConfig{Root: "./var/files"},
    // ProcessingMode omitted: original_only.
}, host.OriginalRuntimeDeps{
    Database: database,
    Transaction: transactionManager,
    Outbox: outboxService,
    Logger: logger,
    // Events may be nil: only best-effort live UI events are disabled.
})
if err != nil {
    return err
}

// Register runtime.Jobs with your outbox worker before accepting uploads.
// Construct host.NewUploadHandler with runtime.Uploader, runtime.Files and
// runtime.TusStore plus your logger, auth context builder and URL composer.
// Run host.MigrationFiles and your outbox backend migrations first.
```

The snippet illustrates the constructor; the infrastructure values belong to
the embedding application. It is not an independent executable starter. The
runtime does not close externally supplied clients, start workers or expose
routes. Its `Storage` is also available for an appropriately authorized read
route. Never serve the entire storage root or expose staging directories.

For S3, use the existing explicit endpoint, region, credentials, bucket,
staging bucket, public delivery and TUS settings. The source is read through
`Storage.Open` against the staging location, not through a presigned HTTP URL
or a `site` proxy. Local TUS remains the existing single-node filesystem store;
this change does not make local TUS cross-node durable.

## State, content and cleanup

All four ingestion paths (multipart single/batch, reader, already-stored/TUS)
use the same configured mode. Ingestion stages the source, creates a queued file
record and enqueues `finalize_original_file` in the same transaction. Returning
from upload means **queued**, not completed. A stopped outbox worker leaves the
file pending; there is no synchronous or lossy in-memory fallback.

The job payload contains only `fileId`. The worker reads the source location
from the locked database row and accepts only canonical keys under configured
staging prefixes (`tmp/uploads` and the TUS staging prefix by default). It never
opens a URL supplied in the job payload. The destination and staging prefixes
must not overlap.

The worker reuses the existing artifact finalizer's content sniffing, MIME and
size checks, streaming SHA-256 calculation, deterministic final key construction,
metadata update, after-job payload construction and staging cleanup. No media
transformation is performed. The single `main` artifact contains the original
bytes; the client filename, known dimensions and moderation fields are retained.
The current finalizer accepts JPEG, PNG, GIF, WebP, MP4, WebM and PDF, matching
the standard upload allowlist. This does not promise arbitrary document types.
An original upload requires a valid object type and a positive object ID.

Configured derived presets or enabled watermarking in `original_only` produce
`host.ErrProcessingDisabled`; they are not silently ignored. Completion does
not mean antivirus clearance, moderation approval or authorization for public
file delivery. Applications still own those policies.

Metadata, configured `AfterJobs`, ownership-bound replacement deletion and exact
staging-key deletion are committed together. The staging cleanup has `FileID=0`;
it does not delete the newly finalized row. The prior file is not scheduled for
deletion until final storage and metadata have succeeded. There is no new
second uploader inside goadmin and no change to the isolated CMS quarantine
promotion or CMS-only TUS routes.

## Retries and concurrency

`GetByIDForUpdate` must execute in the same transaction context as the final
metadata and outbox writes. The original worker holds that row lock while
streaming to final storage. This is deliberately conservative: it serializes
concurrent retries across workers without introducing a new lease table.
Bound worker concurrency, upload sizes and storage timeouts. For large media,
a fenced short-transaction finalizer would be a separate optimization.

A completed or deleted row makes repeated original jobs no-ops. On failure,
staging is retained and the deterministic final key is retryable. In particular,
no deletion of the final key is scheduled after an uncertain commit result: that
could delete a successfully committed file. Permanently failed jobs may leave
staging or unreferenced final objects. Operational cleanup/reconciliation is
still necessary and must not remove sources needed by pending jobs; this PR
adds no blanket orphan sweeper. Live events remain best-effort after commit and
may be absent after a crash; consumers must reconcile against stored file state.

## Migrating an existing media pipeline

1. Register the original finalizer job before enabling the new configured
   uploader. Do not mix a new producer with workers that lack this job name.
2. To retain resize behavior, set `ProcessingMode: host.ProcessingMediaResizer`
   (or `STORAGE_PROCESSING_MODE=media_resizer`) explicitly. Supply resizer job
   URLs, callback URLs and tokens for the existing image/video pipeline. The
   configuration no longer invents `backend` or `media_resizer` hostnames.
3. `SkipResizer` keeps its historical media-payload meaning. It does not switch
   modes and is not an alias for `original_only`.
4. `host.BuildOutboxJobs` accepts original commands, the complete media trio,
   or both. Existing `host.OutboxJobs` retains fail-fast behavior. While old
   media jobs are in flight, retain their handlers, credentials, callback route
   and media service until they drain. Modes are selected at ingestion by job
   name; old payloads are not reinterpreted when configuration changes.
5. Remove preset/watermark requirements when selecting original-only behavior.
   Review consumers expecting thumbnails, transcoding or a particular format.

For compatibility, the old **unsupported deep** `uploadservice.New`/`Must`
constructors retain their historical media behavior and `New` is deprecated.
New integrations must use the stable `host` surface. Configured DI uses
`NewWithProcessing` internally so its zero mode is original-only. No generated
Options constructor or mock was hand-edited, and the supported package list
remains `host` and `hosttest`.

## Release gates

Before merge, use the declared Go/toolchain and existing repository gates:

```sh
make check
make test-surface
make test-tus-postgres-integration
make test-portable-media-e2e
```

Additionally verify the new original pipeline against real PostgreSQL and local
storage, then S3/MinIO, including concurrent duplicate jobs, worker restart,
failed final writes, lost commit acknowledgement, replacement and staging cleanup.
Unit-test transaction fakes are not evidence of actual database locking.

Test the affected goadmin/site consumers separately before changing their pinned
versions. Before tagging, run `make publish-readiness VERSION=<planned-tag>`
with the required services and sibling repositories. This gate runs the mutating
preparation phase; review generated and tidy changes. After approval and actual
publication of that immutable tag, run `make release-readiness VERSION=<tag>`
and the included `externalconsumer-published` gate. That gate is not
an anonymous-public-access guarantee while private build dependencies remain.
