# gouploads

[![Go Reference](https://pkg.go.dev/badge/github.com/assurrussa/gouploads.svg)](https://pkg.go.dev/github.com/assurrussa/gouploads)
[![Go Report Card](https://goreportcard.com/badge/github.com/assurrussa/gouploads)](https://goreportcard.com/report/github.com/assurrussa/gouploads)
[![Go](https://github.com/assurrussa/gouploads/actions/workflows/go.yml/badge.svg)](https://github.com/assurrussa/gouploads/actions/workflows/go.yml)
[![License: MIT](https://img.shields.io/badge/License-MIT-yellow.svg)](https://opensource.org/licenses/MIT)

## Overview

`gouploads` is a powerful, extensible Go library and service designed for handling file uploads, media processing, and storage orchestration. Built with the widely-used [gofiber](https://github.com/gofiber/fiber) framework, `gouploads` excels at managing modern upload complexities out of the box.

Key Features:
- **Resumable Uploads**: Full support for the [Tus](https://tus.io/) protocol, allowing robust, resumable uploads for large files.
- **Storage Agnostic**: Built-in support for both Local file system and S3/Ceph storage via `aws-sdk-go-v2`.
- **Media Processing Pipelines**: Integrated structures for video and image resizing/compression callbacks.
- **Transaction Outbox Integration**: Works seamlessly with `outbox` patterns to guarantee the execution of post-upload background jobs (like resizing, webhook firing, or database cleanup).
- **Flexible Context**: Highly adaptable file upload context building, enabling integrations with any authentication or multi-tenant system.

## Install

```bash
go get github.com/assurrussa/gouploads@latest
```

## Quick Start

`gouploads` uses an interface-driven architecture to wire its components together and register specific processing strategies for different contexts (e.g., standard uploads vs. rich-text uploads).

Host applications should import `github.com/assurrussa/gouploads/host` as the
stable embedding surface. Deep `domain/files/*`, `config`, and `di` packages
are library internals unless a release note explicitly promotes a package.

### 1. Server Initialization

```go
package main

import (
	uploadhost "github.com/assurrussa/gouploads/host"
	"github.com/gofiber/fiber/v3"
)

func main() {
	app := fiber.New()
	
    // 1. Initialize your TaskUploader, FileRepo, TusStore, Logger, etc.
	// handler := uploadhost.NewUploadHandler(taskUploader, fileRepo, tusStore, logger, contextBuilder, urlComposer)
	
	// 2. Register uploadhost.UploadStrategy implementations (optional, but recommended for processing pipelines)
	// handler.RegisterStrategy("avatar", avatarStrategy)
	// handler.RegisterStrategy("rich-text", richTextStrategy)
	
	// 3. Wrap the handler for Fiber
	// wrapper := uploadhost.NewFiberUploadHandler(handler)
	
	// 4. Register routes to a specific group
	// wrapper.RegisterGroupRoutes("/api/v1/uploads", app)
	
	app.Listen(":3000")
}
```

### 2. Client Usage (REST API)

Once connected, you can upload files via standard `multipart/form-data` requests or use the [Tus](https://tus.io/) protocol for resumable uploads.

**Standard Batch Upload:**
```bash
curl -X POST http://localhost:3000/api/v1/uploads \
  -H "Content-Type: multipart/form-data" \
  -F "entity_type=user" \
  -F "entity_id=123" \
  -F "context=avatar" \
  -F "files=@/path/to/profile.jpg"
```

## Configuration

`gouploads` utilizes standard `toml` and `ENV` mapping to configure its internals.

Some of the main configurations include:
- `STORAGE_DRIVER`: `local` or `s3`
- `STORAGE_S3_ENDPOINT`, `STORAGE_S3_BUCKET`, `STORAGE_S3_REGION`
- Sub-pipelines logic per media type (`STORAGE_IMAGE_DEFAULT_FORMAT`, `STORAGE_VIDEO_RESIZER_HOST`, etc).

## Stable Host Surface

The supported host-facing package is:

- `github.com/assurrussa/gouploads/host`

It exposes storage config types, file model contracts, object identifiers, TUS
store contracts, upload handler contracts, resize webhook request helpers,
cleanup use case factories, outbox job registration, and DI bootstrap helpers.
Host projects remain responsible for local environment mapping, auth context
extraction, route mounting, object-type policy, and deployment topology.

The machine-readable source of truth is
`gouploads/reference/externalconsumer`. Host projects should treat packages not
listed there, including `domain/files/*`, `shared/*`, `config`, and `di`, as
internal implementation details.

To check a host repository:

```bash
go run ./cmd/importpolicy --repo-root ../site --allow-deep-dir backend/internal/infrastructure/uploads
```

The `--allow-deep-dir` flag is transitional. It allows an existing host adapter
to keep deep imports until the host can update to a released `gouploads` version
that contains the `host` facade.

## License

MIT
