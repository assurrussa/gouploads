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
- TUS store contracts;
- upload handler and strategy interfaces;
- image/video resize request and callback request contracts;
- upload, resize, listen, and delete outbox job factories;
- cleanup use case factories;
- embedded `files` table migrations exposed by `host.MigrationsFS`.

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

2. Add `host.BootstrapDependencies()` to the host dependency container if the
   host uses `godi`.

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
go run ./cmd/importpolicy --repo-root ../site --consumers backend,goadmin,fixtures/second-go-host
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
