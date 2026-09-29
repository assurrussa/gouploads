# Host Integration Contract

`gouploads` is reusable through two public package paths:

- `github.com/assurrussa/gouploads/host` for runtime embedding.
- `github.com/assurrussa/gouploads/hosttest` for external consumer tests.

All other packages, including `domain/files/*`, `config`, `di`,
`infrastructure/*`, and `shared/*`, are implementation details unless they are
listed in `reference/externalconsumer`.

## Library Ownership

`gouploads` owns:

- upload file models and public file contracts;
- local and S3 storage configuration contracts;
- TUS store contracts, durable S3 multipart session state, and fencing;
- upload handler and strategy interfaces;
- image/video resize request and callback request contracts;
- upload, resize, listen, and delete outbox job factories;
- cleanup use case factories;
- embedded `files` and `upload_sessions` migrations exposed by
  `host.MigrationsFS`.

The outbox backend owns its own job storage migrations. Host projects must run
those migrations in addition to the `gouploads` file migration.

## Host Ownership

Every host application owns:

- local env/config mapping into `host.StorageConfig`;
- database, transaction manager, logger, and event publisher wiring;
- HTTP route mounting and auth/session middlewares;
- upload context extraction from the host auth/session model;
- upload strategies for project-specific contexts such as `avatar`,
  `rich-text`, `gallery`, or `video`;
- outbox service lifecycle and registration of `host.OutboxJobs`;
- migration execution order;
- media-resizer deployment URL, API token, and callback URL.

Host-owned `object_type` values are supported when they are stable lowercase
identifiers: they must start with `a-z`, contain only `a-z`, `0-9`, or `_`, and
be at most 64 bytes. Values such as `post`, `author`, and `media_asset_2` are
valid. Treat these identifiers as persisted storage contracts; do not derive
them from user input or mutable display names.

The media-resizer process is an external service. `gouploads` only needs its
`/jobs` URL and API token, plus the callback URL that the media-resizer can
reach.

For private S3 objects, `host.NewSourceURLResolver` signs `GetObject` at
dispatch time against the configured S3 endpoint and staging bucket. The URL
is passed directly to media-resizer; host rewriting and a source proxy are not
supported. Signed query strings must not be logged. Local storage may configure
`StorageLocalConfig.SourceBaseURL` as the backend origin reachable by
media-resizer; relative temporary paths are then composed below
`tmp/uploads/...`. With an empty source base, the legacy shared-filesystem path
flow remains pass-through.

## Minimal Runtime Wiring

1. Map local configuration into `host.StorageConfig`.

   ```go
   cfg := host.StorageConfig{
       Driver: host.StorageDriverS3,
       ProcessingMode: host.ProcessingMediaResizer,
       Public: host.StoragePublicConfig{
           BaseURL: "https://media.example.test",
           Prefix: "media/v1",
       },
       S3: host.StorageS3Config{
           Endpoint: endpoint,
           SourceURLTTL: 15 * time.Minute,
           Region: region,
           Bucket: bucket,
           StagingBucket: stagingBucket, // optional; empty means Bucket
           AccessKey: accessKey,
           SecretKey: secretKey,
           ForcePathStyle: forcePathStyle,
           Timeout: 30 * time.Second,
           MaxRetries: 10,
       },
       Tus: host.StorageTusConfig{
           PartSize: host.ParseSize("8MB"),
           SessionTTL: 24 * time.Hour,
           LeaseTTL: 30 * time.Second,
           StagingPrefix: "staging/v1/tus",
       },
       Image: host.ImagePipelineConfig{
           ResizerHost: resizerJobsURL,
           WebhookCallbackHost: callbackURL,
           ResizerToken: resizerToken,
       },
       Video: host.VideoPipelineConfig{
           ResizerHost: resizerJobsURL,
           WebhookCallbackHost: callbackURL,
           ResizerToken: resizerToken,
       },
   }
   ```

   A local filesystem host with a separate media-resizer should keep public
   delivery and temporary source delivery independent:

   ```go
   cfg := host.StorageConfig{
       AppDomainURL: "https://app.example.test",
       ProcessingMode: host.ProcessingMediaResizer,
       Driver: host.StorageDriverLocal,
       Local: host.StorageLocalConfig{
           Root: "/app/publicdata",
           BaseURL: "",
           SourceBaseURL: "http://backend:8080",
       },
   }
   ```

   Final files remain below `uploads/...` and use `BaseURL` or
   `AppDomainURL`. Only temporary `tmp/uploads/...` paths use
   `SourceBaseURL`. The host must serve both `/uploads` and `/tmp` from the
   configured local root.

   Provider profiles are ordinary values of that same struct, not presets:

   - local MinIO: `Endpoint=http://s3server:9000`, `Region=us-east-1`,
     `ForcePathStyle=true`, and a delivery base such as
     `http://minio.localhost/media`;
   - Yandex Object Storage: `Endpoint=https://storage.yandexcloud.net`,
     `Region=ru-central1`, `ForcePathStyle=true`, with a separately configured
     custom delivery origin;
   - Selectel path-style: `Endpoint=https://s3.<pool>.storage.selcloud.ru`,
     `Region=<pool>`, `ForcePathStyle=true`, with its own delivery origin.

   Credentials, bucket names, and delivery origins remain host-owned. Do not
   derive behavior from a provider name.

2. Run all `host.MigrationFiles()` and build the TUS store. S3 mode requires
   the same PostgreSQL client used by the host composition root:

   ```go
   tusStore, err := host.NewTusStore(cfg, database)
   ```

   Add `host.BootstrapDependencies()` to the host dependency container if the
   host uses `godi`; its TUS provider has the same database requirement.

3. Build an upload handler with host-owned auth context extraction.

   ```go
   handler := host.NewUploadHandler(
       uploader,
       fileRepo,
       tusStore,
       logger,
       func(ctx context.Context, metadata map[string]string) (host.UploadContext, error) {
           auth := currentAuth(ctx)
           return host.UploadContext{
               UserID: auth.ID,
               UserUUID: auth.UUID,
               SessionID: auth.SessionID,
               Metadata: metadata,
           }, nil
       },
       func(path string) string {
           return host.ComposeFileURL(host.FilesBaseURL(cfg), host.FilesBucket(cfg), path)
       },
   )
   handler.RegisterStrategy("default", defaultStrategy)
   handler.RegisterStrategy("rich-text", richTextStrategy)
   ```

4. Mount the transport adapter.

   ```go
   host.NewFiberUploadHandler(handler).RegisterGroupRoutes("/files", app)
   ```

5. Mount the media-resizer callback route in the host API and convert the HTTP
   request body into `host.BuildListenResizeRequest(...)`, then call
   `host.ListenResizeHandler`.

6. Register background upload jobs in the host outbox service.

   ```go
   for _, job := range host.OutboxJobs(host.OutboxJobDeps{
       Logger: logger,
       UseCaseSendResize: sendResize,
       UseCaseListenResize: listenResize,
       UseCaseUploadFile: uploadFile,
       UseCaseDeleteFile: deleteFile,
   }) {
       outbox.MustRegisterJob(job)
   }
   ```

7. Register cleanup tasks with `host.NewCleanFilesUseCase` and
   `host.NewCleanTusUseCase` if the host runs scheduled cleanup.

## Migrations

Hosts can read the file table migrations directly from the public host facade:

```go
migrationFS, err := host.MigrationsFS()
files, err := host.MigrationFiles()
```

Use these files with an `fs.FS` capable migration runner, or copy the SQL into
the host migration tree during release preparation. Keep the version prefix
stable so repeated host deployments do not create duplicate migration history.

The host must also run the outbox backend migrations used by its outbox storage
implementation.

The `upload_sessions` migration is mandatory for S3 TUS. Protocol metadata,
part checksums, offset, lease owner, monotonic fencing revision, expiry, and the
stable finalization key live in PostgreSQL. Redis may still be used elsewhere
by the host, but it is not the source of truth for TUS sessions.

## Durable S3 TUS Semantics

- Each PATCH claims a short database-clock lease and increments `revision`.
- Only the matching lease owner and revision can commit the uploaded part and
  advance `Upload-Offset`; stale replicas receive a conflict.
- A retry after a crash compares part number, size, ETag, and SHA-256 through
  S3 `ListParts` before reusing an already uploaded part. Finalization requires
  the durable part number, size, and ETag to match; it also compares SHA-256
  when the S3-compatible provider includes that optional field in `ListParts`.
  Providers such as Yandex Object Storage omit it from that response.
- Finalization is fenced and repeatable. A completed object is recovered with
  `HeadObject` if the process died after S3 completion but before the database
  commit. `FinalizationKey` is stable across retries for downstream idempotency.
- Incomplete objects use `Cache-Control: private,no-store` in the staging
  bucket/prefix. No object ACL is sent. Protocol completion does not make an
  object public; media finalization writes immutable artifacts under the
  configured public prefix.
- Cleanup also lists old multipart uploads under that prefix and aborts entries
  that have no matching durable session. The cleanup threshold protects an
  in-flight CreateMultipartUpload-to-database-insert window.
- PostgreSQL sessions already store `expires_at = last_activity + SessionTTL`.
  The S3 cleanup adapter converts the host activity threshold before comparing
  it with `expires_at`, so an abandoned session waits one TTL plus the nearest
  scheduler interval, not two TTL periods.
- `STORAGE_TUS_PART_SIZE` is the exact size of every non-final PATCH. The final
  PATCH may be smaller. The transport rejects concurrent offsets and invalid
  intermediate chunk sizes.

## Delivery and live storage check

`StoragePublicConfig.BaseURL` is the complete delivery prefix; it may be an
origin such as `https://media.example.test` or a local path-style prefix such
as `http://minio.localhost/media`. `FilesBucket` is intentionally empty for the
portable contract. Final object keys and DB rows therefore survive a provider
or DNS change without rewriting content.

Hosts should expose an explicit operator command around:

```go
checker, err := host.NewStorageContractChecker(cfg)
report, err := checker.Check(ctx)
```

Do not run it at application startup. The checker creates unique staging and
final probe keys, never calls `ListBucket`, validates anonymous/private and
presigned/public reads including Range and immutable cache metadata, then
attempts authenticated cleanup even after failure.

## Test Support

Consumer tests may import `github.com/assurrussa/gouploads/hosttest` for stable
test-only contracts such as storage payload aliases and resize callback
matchers. Those aliases are part of the `hosttest` contract; consumers should
still import them through `hosttest`, not through `infrastructure/storage/*` or
other internal package paths.

When a consumer builds with `-tags integration`, `hosttest` also exposes
PostgreSQL database helpers:

```go
pool, db, cleanup := hosttest.PrepareDB(ctx, t, "uploads_test",
    hosttest.WithDatabasePathFilesMigration("db/migrations"),
)
t.Cleanup(func() { cleanup(context.Background()) })
```

The integration helpers read the same `TEST_PSQL_*` environment variables used
by the existing site integration tests:

- `TEST_PSQL_ADDRESS` / `TEST_PSQL_ADDRESS_LOCAL`;
- `TEST_PSQL_PORT` / `TEST_PSQL_PORT_LOCAL`;
- `TEST_PSQL_USERNAME`;
- `TEST_PSQL_PASSWORD`;
- `TEST_PSQL_DATABASENAME`;
- `TEST_PSQL_SSL_MODE`.

These helpers are not available in normal builds, so host runtime code must not
depend on them. The external consumer probe compiles both normal and
`-tags integration` test modules to keep the split verified.

## Boundary Checks

Run the import policy against host repositories:

```bash
go run ./cmd/importpolicy --repo-root .. --consumers site/backend,goadmin,site/fixtures/second-go-host
```

Runtime host code should only import `gouploads/host`. Test files and packages
under `tests` or `testsupport` may import `gouploads/hosttest`. Deep
`gouploads` imports in host runtime or test code are a boundary regression.
Requested consumer roots are required to exist so the check cannot pass after a
bad `--repo-root` or stale consumer path.
The external consumer probe checks both normal and `-tags integration` builds
so integration-only host test helpers stay part of the verified contract.
`hosttest` owns its public database helper types directly; host consumers
should not import `domain/files/tests`.

Identity, notification and HTTP-client migration is documented in
[Standalone uploads](standalone-uploads.md#identity-and-notification-migration).
