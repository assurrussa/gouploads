package host

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strconv"
	"sync"
	"sync/atomic"

	"github.com/gofiber/fiber/v3"
	"github.com/gofiber/fiber/v3/middleware/adaptor"
)

const (
	// DefaultBodyLimit is the default maximum request body size (32 MiB)
	// for StandardUploadHandler, accommodating standard 5 MiB TUS chunks and common uploads
	// without excessive memory consumption.
	DefaultBodyLimit = 32 * 1024 * 1024

	internalCtxIDHeader = "X-Internal-Gouploads-Ctx-ID"
)

// ErrIncompatibleBodyLimit is returned by NewStandardUploadHandler when the effective
// body limit is smaller than the underlying TUS store's chunk size.
var ErrIncompatibleBodyLimit = errors.New("tus chunk size exceeds effective body limit")

// ErrNilUploadHandler is returned by NewStandardUploadHandler when the upload
// handler is nil or uninitialized. Create it with NewUploadHandler or
// NewUploadHandlerWithPolicy before constructing the standard adapter.
var ErrNilUploadHandler = errors.New("upload handler is nil or uninitialized")

// ErrInvalidStandardHandlerConfig is returned by NewStandardUploadHandler when
// more than one StandardUploadHandlerConfig is supplied.
var ErrInvalidStandardHandlerConfig = errors.New("expected at most one standard upload handler config")

// StandardUploadHandlerConfig configures StandardUploadHandler.
type StandardUploadHandlerConfig struct {
	// BodyLimit specifies the maximum size of the entire HTTP request body in bytes.
	// For multipart uploads, allow framing and part-header overhead beyond the
	// individual file size limit.
	// If <= 0, DefaultBodyLimit (32 MiB) is used.
	// Note: BodyLimit must be at least as large as the underlying TUS store's ChunkSize,
	// otherwise NewStandardUploadHandler returns ErrIncompatibleBodyLimit.
	BodyLimit int
}

// StandardUploadHandler wraps an UploadHandler as a net/http-compatible adapter,
// making it mountable directly where http.Handler is supported, or through a
// router's standard http.Handler adapter.
type StandardUploadHandler struct {
	app        *fiber.App
	handler    http.HandlerFunc
	requests   sync.Map
	reqCounter atomic.Uint64
}

// NewStandardUploadHandler creates an HTTP handler mounting upload routes under prefix.
// The resulting handler implements http.Handler. Mount it directly where
// http.Handler is supported (http.ServeMux, Chi), or through the router's standard
// http.Handler adapter (Gin, Echo).
// It accepts at most one configuration, returning ErrInvalidStandardHandlerConfig
// for multiple configurations and ErrNilUploadHandler for a nil or uninitialized handler.
// Returns ErrIncompatibleBodyLimit if the underlying TUS store's chunk size exceeds the effective body limit.
func NewStandardUploadHandler(
	uploadHandler *UploadHandler,
	prefix string,
	cfgs ...StandardUploadHandlerConfig,
) (*StandardUploadHandler, error) {
	if uploadHandler == nil || uploadHandler.inner == nil {
		return nil, ErrNilUploadHandler
	}
	if len(cfgs) > 1 {
		return nil, ErrInvalidStandardHandlerConfig
	}

	bodyLimit := DefaultBodyLimit
	if len(cfgs) > 0 && cfgs[0].BodyLimit > 0 {
		bodyLimit = cfgs[0].BodyLimit
	}

	if chunkSize := uploadHandler.TusChunkSize(); chunkSize > 0 && chunkSize > int64(bodyLimit) {
		return nil, fmt.Errorf(
			"%w: tus chunk size %d exceeds body limit %d; "+
				"configure StandardUploadHandlerConfig.BodyLimit explicitly",
			ErrIncompatibleBodyLimit, chunkSize, bodyLimit,
		)
	}

	app := fiber.New(fiber.Config{
		BodyLimit: bodyLimit,
	})

	h := &StandardUploadHandler{
		app: app,
	}

	// Middleware to propagate r.Context() from ServeHTTP into Fiber's context.
	app.Use(func(c fiber.Ctx) error {
		if id := c.Get(internalCtxIDHeader); id != "" {
			if val, ok := h.requests.Load(id); ok {
				if stdCtx, ok := val.(context.Context); ok && stdCtx != nil {
					c.SetContext(stdCtx)
				}
			}
			// Do not leak internal propagation header downstream
			c.Request().Header.Del(internalCtxIDHeader)
		}
		return c.Next()
	})

	fiberHandler := NewFiberUploadHandler(uploadHandler)
	if prefix == "" {
		prefix = "/"
	}
	fiberHandler.RegisterGroupRoutes(prefix, app)

	h.handler = adaptor.FiberApp(app)
	return h, nil
}

// ServeHTTP implements http.Handler.
func (h *StandardUploadHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	reqID := strconv.FormatUint(h.reqCounter.Add(1), 10)
	h.requests.Store(reqID, r.Context())
	defer h.requests.Delete(reqID)

	prevID := r.Header.Get(internalCtxIDHeader)
	defer func() {
		if prevID != "" {
			r.Header.Set(internalCtxIDHeader, prevID)
		} else {
			r.Header.Del(internalCtxIDHeader)
		}
	}()

	r.Header.Set(internalCtxIDHeader, reqID)
	h.handler(w, r)
}
