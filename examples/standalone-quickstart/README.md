# gouploads Standalone Quickstart

This example demonstrates running `gouploads` as an embedded upload orchestration service with:
- **PostgreSQL 16**: metadata storage and zero-Redis TUS fencing via transactional advisory locks.
- **MinIO**: S3-compatible object storage with isolated public (`uploads-public`) and private staging (`uploads-staging`) buckets.
- **Transactional Outbox**: background staging-to-final copy, SHA-256 calculation, and safe file cleanup.
- **net/http-compatible adapter**: mounted onto standard Go HTTP server (also works with Chi, Gin, Echo).
- **Web UI**: modern drag & drop upload interface powered by [tus-js-client v4.3.1](https://github.com/tus/tus-js-client).

> [!WARNING]
> This quickstart is designed strictly for local demonstration and development (`127.0.0.1:8080`). In production, configure proper authentication middleware, route guards, explicit `UploadHandlerPolicy` authorizers, and restricted bucket access.

## Quick Start (in 2 minutes)

Use a source checkout and run every command below from the example directory:

```bash
cd /path/to/gouploads/examples/standalone-quickstart
```

The example uses that checkout's root `go.mod`, not a separately installed
`gouploads` release. Use the Go version/toolchain declared there (currently
Go `1.27.0`, toolchain `go1.27.1`) and have Docker Compose available.

### 1. Start PostgreSQL and MinIO

```bash
docker compose up -d
```

This starts:
- PostgreSQL on `127.0.0.1:5432` (`postgres:password`, database `gouploads`)
- MinIO S3 API on `127.0.0.1:9000` (`minioadmin:minioadmin`)
- MinIO Web Console on `127.0.0.1:9001`
- Automatically creates `uploads-public` (anonymous download enabled) and `uploads-staging` (strictly private)

### 2. Run the application

```bash
go run main.go
```

The application will:
1. Connect to PostgreSQL and automatically apply outbox and gouploads migrations.
2. Initialize `host.NewOriginalRuntime` with S3 storage driver.
3. Start the outbox background worker for finalization jobs.
4. Mount `host.NewStandardUploadHandler` onto standard `http.ServeMux` at `/files/`.
5. Start listening on `http://127.0.0.1:8080`.

### 3. Open Web UI

Open [http://localhost:8080](http://localhost:8080) in your browser:
- Drag and drop an image (`.jpg`, `.png`, `.webp`, `.gif`), video (`.mp4`, `.webm`), or document (`.pdf`) up to 500 MB.
- Observe real-time chunked TUS upload (5 MiB chunks) with progress bar.
- Click **Pause** and **Resume** to verify resumable uploads.
- Upon completion, the client posts to the returned TUS upload URL plus `/complete` (202 Accepted), polls `GET /files/tasks/:id` until outbox finalization is complete, and displays the direct public storage link to the finalized artifact.
