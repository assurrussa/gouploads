package host

import (
	"context"

	logger "github.com/assurrussa/gologger"
	"github.com/gofiber/fiber/v3"

	uploadhttp "github.com/assurrussa/gouploads/domain/files/transport/http"
	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

type TaskUploader interface {
	UploadBatch(ctx context.Context, req BatchRequest) ([]File, error)
	UploadSingle(ctx context.Context, req SingleRequest) (File, error)
	UploadReader(ctx context.Context, req ReaderRequest, input ReaderUploadInput) (File, error)
	UploadStored(ctx context.Context, req ReaderRequest, uploaded UploadedFile) (File, error)
	DeleteFile(ctx context.Context, req DeleteRequest) error
	GetFile(ctx context.Context, fileID int64) (File, error)
}

type FileRepository interface {
	GetByID(ctx context.Context, id int64) (File, error)
	List(ctx context.Context, filters ListFilters) ([]File, int, error)
}

type ContextBuilder func(ctx context.Context, metadata map[string]string) (UploadContext, error)

type URLComposer func(path string) string

type UploadContext struct {
	UserID    int64
	UserUUID  UserID
	SessionID string
	Metadata  map[string]string
}

type UploadStrategy interface {
	CanUpload(ctx context.Context, req UploadContext) error
	GetConfig(ctx context.Context, req UploadContext) *FileUploadConfig
	GetAfterJobs(ctx context.Context, req UploadContext) ([]FileEventAfterJob, error)
}

type UploadHandler struct {
	inner *uploadhttp.Handler
}

func NewUploadHandler(
	taskUploader TaskUploader,
	fileRepo FileRepository,
	tusStore TusStore,
	lg logger.Logger,
	contextBuilder ContextBuilder,
	urlComposer URLComposer,
) *UploadHandler {
	return &UploadHandler{
		inner: uploadhttp.NewHandler(
			taskUploader,
			fileRepo,
			tusStore,
			lg,
			internalContextBuilder(contextBuilder),
			uploadhttp.URLComposer(urlComposer),
		),
	}
}

func (h *UploadHandler) RegisterStrategy(contextName string, strategy UploadStrategy) {
	h.inner.RegisterStrategy(contextName, uploadStrategyAdapter{strategy: strategy})
}

// TusChunkSize returns the configured chunk size of the underlying TUS store,
// or 0 if not defined.
func (h *UploadHandler) TusChunkSize() int64 {
	if h != nil && h.inner != nil {
		return h.inner.TusChunkSize()
	}
	return 0
}

type FiberUploadHandler struct {
	inner *uploadhttp.FiberUploadHandlerWrapper
}

// UploadRouteGuards keeps read, create/resume, and delete authorization
// independent. Common handlers run before every route in the group.
type UploadRouteGuards struct {
	Common []fiber.Handler
	Read   []fiber.Handler
	Create []fiber.Handler
	Delete []fiber.Handler
}

func NewFiberUploadHandler(handler *UploadHandler) *FiberUploadHandler {
	return &FiberUploadHandler{
		inner: uploadhttp.NewFiberUploadHandlerWrapper(handler.inner),
	}
}

func (h *FiberUploadHandler) RegisterStrategy(contextName string, strategy UploadStrategy) {
	h.inner.RegisterStrategy(contextName, uploadStrategyAdapter{strategy: strategy})
}

func (h *FiberUploadHandler) RegisterGroupRoutes(prefix string, router fiber.Router, middlewares ...fiber.Handler) {
	h.inner.RegisterGroupRoutes(prefix, router, middlewares...)
}

func (h *FiberUploadHandler) RegisterGroupRoutesWithGuards(
	prefix string,
	router fiber.Router,
	guards UploadRouteGuards,
) {
	h.inner.RegisterGroupRoutesWithGuards(prefix, router, uploadhttp.RouteGuards{
		Common: guards.Common,
		Read:   guards.Read,
		Create: guards.Create,
		Delete: guards.Delete,
	})
}

// RegisterCMSTusRoutes mounts OPTIONS/POST/HEAD/PATCH under prefix/tus.
// It deliberately omits generic completion, listing, file reads, and delete.
func (h *FiberUploadHandler) RegisterCMSTusRoutes(
	prefix string,
	router fiber.Router,
	middlewares ...fiber.Handler,
) {
	h.inner.RegisterCMSTusRoutes(prefix, router, middlewares...)
}

type uploadStrategyAdapter struct {
	strategy UploadStrategy
}

func (a uploadStrategyAdapter) CanUpload(ctx context.Context, req uploadstrategies.UploadContext) error {
	return a.strategy.CanUpload(ctx, toHostUploadContext(req))
}

func (a uploadStrategyAdapter) GetConfig(ctx context.Context, req uploadstrategies.UploadContext) *FileUploadConfig {
	return a.strategy.GetConfig(ctx, toHostUploadContext(req))
}

func (a uploadStrategyAdapter) GetAfterJobs(
	ctx context.Context,
	req uploadstrategies.UploadContext,
) ([]FileEventAfterJob, error) {
	return a.strategy.GetAfterJobs(ctx, toHostUploadContext(req))
}

func internalContextBuilder(builder ContextBuilder) uploadhttp.ContextBuilder {
	if builder == nil {
		return nil
	}

	return func(ctx context.Context, metadata map[string]string) (uploadstrategies.UploadContext, error) {
		req, err := builder(ctx, metadata)
		if err != nil {
			return uploadstrategies.UploadContext{}, err
		}

		return uploadstrategies.UploadContext{
			UserID:    req.UserID,
			UserUUID:  req.UserUUID,
			SessionID: req.SessionID,
			Metadata:  req.Metadata,
		}, nil
	}
}

func toHostUploadContext(req uploadstrategies.UploadContext) UploadContext {
	return UploadContext{
		UserID:    req.UserID,
		UserUUID:  req.UserUUID,
		SessionID: req.SessionID,
		Metadata:  req.Metadata,
	}
}
