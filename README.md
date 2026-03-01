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

### 1. Server Initialization

```go
package main

import (
	"context"
	
	"github.com/gofiber/fiber/v3"
	http_transport "github.com/assurrussa/gouploads/domain/files/transport/http"
	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

func main() {
	app := fiber.New()
	
    // 1. Initialize your TaskUploader, FileRepo, TusStore, Logger, etc.
	// handler := http_transport.NewHandler(taskUploader, fileRepo, tusStore, logger, contextBuilder, urlComposer)
	
	// 2. Register Upload Strategies (optional, but recommended for processing pipelines)
	// handler.RegisterStrategy("avatar", &uploadstrategies.AvatarUploadStrategy{ /* ... */ })
	// handler.RegisterStrategy("rich-text", &uploadstrategies.RichTextUploadStrategy{ /* ... */ })
	
	// 3. Wrap the handler for Fiber
	// wrapper := http_transport.NewFiberUploadHandlerWrapper(handler)
	
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

## License

MIT
