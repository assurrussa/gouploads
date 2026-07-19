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
- database, Redis, transaction manager, logger, and event stream wiring;
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

## Minimal Runtime Wiring

1. Map local configuration into `host.StorageConfig`.

   ```go
   cfg := host.StorageConfig{
       Driver: host.StorageDriverS3,
       S3: host.StorageS3Config{
           Endpoint: endpoint,
           Region: region,
           Bucket: bucket,
           AccessKey: accessKey,
           SecretKey: secretKey,
       },
       Tus: host.StorageTusConfig{
           PartSize: host.ParseSize("8MB"),
           SessionTTL: 24 * time.Hour,
           LeaseTTL: 30 * time.Second,
           QuarantinePrefix: "quarantine/uploads",
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
- Incomplete objects use private ACL and the configured quarantine prefix.
  Protocol completion does not make an object public; validation/promotion is
  owned by the host media layer.
- Cleanup also lists old multipart uploads under that prefix and aborts entries
  that have no matching durable session. The cleanup threshold protects an
  in-flight CreateMultipartUpload-to-database-insert window.
- `STORAGE_TUS_PART_SIZE` is the exact size of every non-final PATCH. The final
  PATCH may be smaller. The transport rejects concurrent offsets and invalid
  intermediate chunk sizes.

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
