package http

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	fileshared "github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/internal/filepolicy"
	"github.com/assurrussa/gouploads/internal/filesanitize"
	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

//go:generate toolsmocks

type TaskUploader interface {
	UploadBatch(ctx context.Context, req uploadservice.BatchRequest) ([]model.File, error)
	UploadSingle(ctx context.Context, req uploadservice.SingleRequest) (model.File, error)
	UploadReader(ctx context.Context, req uploadservice.ReaderRequest, input uploadservice.ReaderUploadInput) (model.File, error)
	UploadStored(ctx context.Context, req uploadservice.ReaderRequest, uploaded uploadservice.UploadedFile) (model.File, error)
	DeleteFile(ctx context.Context, req uploadservice.DeleteRequest) error
	GetFile(ctx context.Context, fileID int64) (model.File, error)
}
type FileRepository interface {
	GetByID(ctx context.Context, id int64) (model.File, error)
	List(ctx context.Context, filters filerepo.ListFilters) ([]model.File, int, error)
}
type (
	ContextBuilder func(ctx context.Context, metadata map[string]string) (uploadstrategies.UploadContext, error)
	URLComposer    func(path string) string
)

//nolint:containedctx // requestContext delegates to Fiber Locals when key is missing in Context
type requestContext struct {
	context.Context
	c fiber.Ctx
}

func (r requestContext) Value(key any) any {
	if val := r.Context.Value(key); val != nil {
		return val
	}
	if r.c != nil {
		return r.c.Locals(key)
	}
	return nil
}

func reqCtx(c fiber.Ctx) context.Context {
	if c == nil {
		return context.Background()
	}
	return requestContext{
		Context: c.Context(),
		c:       c,
	}
}

type Handler struct {
	taskUploader   TaskUploader
	fileRepo       FileRepository
	tusStore       tusupload.Store
	logger         logger.Logger
	contextBuilder ContextBuilder
	urlComposer    URLComposer
	strategyMu     sync.RWMutex
	strategies     map[string]uploadstrategies.Strategy
	policy         HandlerPolicy
}

// NewHandler is the compatibility constructor: the empty context uses the
// default strategy and object authorization remains host-owned. Unknown named
// contexts are rejected. New integrations should use NewHandlerWithPolicy,
// whose zero policy denies missing strategies and missing authorization.
func NewHandler(
	taskUploader TaskUploader,
	fileRepo FileRepository,
	tusStore tusupload.Store,
	lg logger.Logger,
	contextBuilder ContextBuilder,
	urlComposer URLComposer,
) *Handler {
	return NewHandlerWithPolicy(taskUploader,
		fileRepo,
		tusStore,
		lg,
		contextBuilder,
		urlComposer,
		HandlerPolicy{
			AllowDefaultStrategy: true,
			TrustRouteGuards:     true,
		})
}

func NewHandlerWithPolicy(
	taskUploader TaskUploader,
	fileRepo FileRepository,
	tusStore tusupload.Store,
	lg logger.Logger,
	contextBuilder ContextBuilder,
	urlComposer URLComposer,
	policy HandlerPolicy,
) *Handler {
	if lg == nil {
		lg = logger.Discard()
	}
	if contextBuilder == nil {
		contextBuilder = func(context.Context, map[string]string) (uploadstrategies.UploadContext, error) {
			return uploadstrategies.UploadContext{}, errors.New("upload identity is not configured")
		}
	}
	if urlComposer == nil {
		urlComposer = func(p string) string { return p }
	}
	return &Handler{
		taskUploader:   taskUploader,
		fileRepo:       fileRepo,
		tusStore:       tusStore,
		logger:         lg,
		contextBuilder: contextBuilder,
		urlComposer:    urlComposer,
		strategies:     make(map[string]uploadstrategies.Strategy),
		policy:         policy,
	}
}

func (h *Handler) RegisterStrategy(contextName string, strategy uploadstrategies.Strategy) {
	h.strategyMu.Lock()
	defer h.strategyMu.Unlock()
	key := normalizedContext(contextName)
	if strategy == nil {
		delete(h.strategies, key)
		return
	}
	h.strategies[key] = strategy
}

func (h *Handler) getStrategy(contextName string) uploadstrategies.Strategy {
	h.strategyMu.RLock()
	defer h.strategyMu.RUnlock()
	return h.strategies[normalizedContext(contextName)]
}

func (h *Handler) TusOptions(c fiber.Ctx) error {
	setTusHeaders(c)
	c.Set("Tus-Version", tusupload.Version)
	c.Set("Tus-Extension", tusupload.Extension)
	c.Set("Access-Control-Allow-Methods", "OPTIONS, POST, HEAD, PATCH")
	c.Set("Access-Control-Allow-Headers",
		"Tus-Resumable, Upload-Length, Upload-Offset, Upload-Metadata, Content-Type, Content-Length, X-CSRF-Token")
	if store, ok := h.tusStore.(interface{ ChunkSize() int64 }); ok {
		c.Set("Upload-Chunk-Size", strconv.FormatInt(store.ChunkSize(), 10))
	}
	return c.SendStatus(http.StatusNoContent)
}
func (h *Handler) TusCreate(c fiber.Ctx) error    { return h.tusCreate(c, false) }
func (h *Handler) TusCreateCMS(c fiber.Ctx) error { return h.tusCreate(c, true) }
func (h *Handler) tusCreate(c fiber.Ctx, cmsOnly bool) error {
	setTusHeaders(c)
	if !isTusResumable(c) {
		return c.SendStatus(http.StatusPreconditionFailed)
	}
	if c.Get("Upload-Defer-Length") != "" {
		return h.jsonError(c, 400, "deferred length is not supported")
	}
	length, err := parseTusLength(c.Get("Upload-Length"))
	if err != nil {
		return h.jsonError(c, 400, "invalid upload length")
	}
	metadata, err := parseTusMetadata(c.Get("Upload-Metadata"))
	if err != nil {
		return h.jsonError(c, 400, "invalid upload metadata")
	}
	filename := strings.TrimSpace(metadataValue(metadata, "filename", "file_name", "fileName"))
	if filename == "" {
		return h.jsonError(c, 400, "filename is required")
	}
	actor, err := h.contextBuilder(reqCtx(c), metadata)
	if err != nil {
		return h.jsonError(c, 401, "upload identity is invalid")
	}
	if actor.UserID <= 0 && actor.UserUUID.IsZero() && !h.policy.AllowAnonymousTUS {
		return h.jsonError(c, 401, "upload actor is required")
	}
	contextName := normalizedContext(metadataValue(metadata, "context"))
	var objectType fileshared.FileObjectType
	var objectID fileshared.FileObjectID
	if cmsOnly {
		if contextName != "cms" {
			return h.jsonError(c, 400, "CMS upload context is required")
		}
		if h.getStrategy(contextName) == nil {
			return h.jsonError(c, 500, "CMS upload strategy is not configured")
		}
	} else {
		objectType, objectID, err = h.parseTusObject(metadata)
		if err != nil {
			return h.jsonError(c, 400, err.Error())
		}
	}
	if err := h.authorize(reqCtx(c), actor, "upload", requestObject(objectType.String(), objectID.Int64())); err != nil {
		return h.writeRequestError(c, err)
	}
	fileTypeValue := normalizedContext(metadataValue(metadata, "file_type"))
	skip := parseBoolFlag(metadataValue(metadata, "skip_resize"))
	resolution, err := h.resolveUploadStrategy(reqCtx(c),
		actor,
		objectType,
		objectID,
		contextName,
		model.GetFileTypeString(fileTypeValue),
		skip,
		resolveUploadStrategyOptions{checkCanUpload: true})
	if err != nil {
		return h.writeStrategyError(c, err)
	}
	if length > resolution.config.MaxFileSize || length > filepolicy.MaxFileSize {
		return h.jsonError(c, 413, "file is too large")
	}
	ext := strings.ToLower(filepath.Ext(filename))
	if !isAllowedExtension(ext, resolution.config.AllowedExtensions) {
		return h.jsonError(c, 400, "file extension is not allowed")
	}
	name := uuid.NewString() + ext
	sessionMeta := map[string]string{
		"file_name":   name,
		"filename":    filename,
		"context":     contextName,
		"file_type":   fileTypeValue,
		"skip_resize": strconv.FormatBool(skip),
		"replace_file_id": strconv.FormatInt(parseInt64(metadataValue(metadata,
			"replace_file_id",
			"deleteId",
			"delete_id")),
			10),
	}
	if !cmsOnly {
		sessionMeta["entity_type"] = objectType.String()
		sessionMeta["entity_id"] = objectID.String()
	}
	session, err := h.tusStore.Create(reqCtx(c),
		tusupload.CreateRequest{
			UploadLength: length,
			OriginalName: filename,
			FileName:     name,
			Metadata:     sessionMeta,
			OwnerID:      actor.UserID,
			OwnerUUID:    actor.UserUUID,
		})
	if err != nil {
		h.logger.ErrorContext(c, "create tus upload", logger.Error(err))
		return h.jsonError(c, 500, "failed to create upload")
	}
	c.Set("Location", strings.TrimRight(c.Path(), "/")+"/"+session.ID)
	if store, ok := h.tusStore.(interface{ ChunkSize() int64 }); ok {
		c.Set("Upload-Chunk-Size", strconv.FormatInt(store.ChunkSize(), 10))
	}
	return c.SendStatus(http.StatusCreated)
}

func (h *Handler) TusHead(c fiber.Ctx) error {
	setTusHeaders(c)
	if !isTusResumable(c) {
		return c.SendStatus(http.StatusPreconditionFailed)
	}
	session, err := h.loadTus(c)
	if err != nil {
		return h.writeRequestError(c, err)
	}
	actor, err := h.contextBuilder(reqCtx(c), session.Metadata)
	if err != nil {
		return h.jsonError(c, 401, "upload identity is invalid")
	}
	if !h.isTusOwner(actor, session) {
		return c.SendStatus(http.StatusForbidden)
	}
	if err := h.authorize(reqCtx(c), actor, "resume", tusObject(session)); err != nil {
		return h.writeRequestError(c, err)
	}
	c.Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
	c.Set("Upload-Length", strconv.FormatInt(session.UploadLength, 10))
	return c.SendStatus(http.StatusOK)
}

func (h *Handler) loadTus(c fiber.Ctx) (tusupload.Session, error) {
	id := c.Params("id")
	parsed, err := uuid.Parse(id)
	if err != nil || parsed == uuid.Nil || parsed.String() != id {
		return tusupload.Session{}, reject(404, "upload not found")
	}
	session, err := h.tusStore.Get(reqCtx(c), id)
	if err != nil {
		if errors.Is(err, tusupload.ErrNotFound) {
			return session, reject(404, "upload not found")
		}
		h.logger.ErrorContext(c, "load tus upload", logger.Error(err))
		return session, reject(500, "failed to load upload")
	}
	return session, nil
}

func tusObject(session tusupload.Session) model.File {
	return requestObject(metadataValue(session.Metadata,
		"entity_type",
		"object_type",
		"objectType"),
		parseInt64(metadataValue(session.Metadata,
			"entity_id",
			"object_id",
			"objectId")))
}
func (h *Handler) TusPatch(c fiber.Ctx) error    { return h.tusPatch(c, false) }
func (h *Handler) TusPatchCMS(c fiber.Ctx) error { return h.tusPatch(c, true) }
func (h *Handler) tusPatch(c fiber.Ctx, cmsOnly bool) error {
	setTusHeaders(c)
	if !isTusResumable(c) {
		return c.SendStatus(http.StatusPreconditionFailed)
	}
	if !strings.EqualFold(strings.TrimSpace(c.Get("Content-Type")), tusupload.ContentType) {
		return c.SendStatus(http.StatusUnsupportedMediaType)
	}
	offset, err := parseTusOffset(c.Get("Upload-Offset"))
	if err != nil {
		return h.jsonError(c, 400, "invalid upload offset")
	}
	session, err := h.loadTus(c)
	if err != nil {
		return h.writeRequestError(c, err)
	}
	actor, err := h.contextBuilder(reqCtx(c), session.Metadata)
	if err != nil {
		return h.jsonError(c, 401, "upload identity is invalid")
	}
	if !h.isTusOwner(actor, session) {
		return c.SendStatus(http.StatusForbidden)
	}
	if err := h.authorize(reqCtx(c), actor, "resume", tusObject(session)); err != nil {
		return h.writeRequestError(c, err)
	}
	if offset != session.Offset {
		c.Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
		return c.SendStatus(http.StatusConflict)
	}
	body := c.Body()
	if offset > session.UploadLength || int64(len(body)) > session.UploadLength-offset {
		c.Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
		return c.SendStatus(http.StatusRequestEntityTooLarge)
	}
	// An empty PATCH is a protocol no-op and does not need MIME detection.
	if len(body) == 0 {
		c.Set("Upload-Offset", strconv.FormatInt(offset, 10))
		return c.SendStatus(http.StatusNoContent)
	}
	mimeType, err := h.resolveTusPatchMimeType(c, session, body, offset, cmsOnly)
	if err != nil {
		return h.writeRequestError(c, err)
	}
	newOffset, err := h.tusStore.Append(reqCtx(c), session.ID, offset, body, mimeType)
	if err != nil {
		return h.handleTusAppendError(c, session, err)
	}
	c.Set("Upload-Offset", strconv.FormatInt(newOffset, 10))
	return c.SendStatus(http.StatusNoContent)
}

func (h *Handler) handleTusAppendError(c fiber.Ctx, session tusupload.Session, err error) error {
	switch {
	case errors.Is(err,
		tusupload.ErrOffsetMismatch),
		errors.Is(err,
			tusupload.ErrUploadBusy),
		errors.Is(err,
			tusupload.ErrFenceLost),
		errors.Is(err,
			tusupload.ErrUploadFinalized):
		offset := session.Offset
		if current, getErr := h.tusStore.Get(reqCtx(c), session.ID); getErr == nil {
			offset = current.Offset
		}
		c.Set("Upload-Offset", strconv.FormatInt(offset, 10))
		return c.SendStatus(http.StatusConflict)
	case errors.Is(err, tusupload.ErrNotFound):
		return c.SendStatus(http.StatusNotFound)
	case errors.Is(err, tusupload.ErrLengthExceeded):
		c.Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
		return c.SendStatus(http.StatusRequestEntityTooLarge)
	case errors.Is(err, tusupload.ErrChunkTooSmall):
		return h.jsonError(c, 400, "chunk size too small")
	case errors.Is(err, tusupload.ErrChunkSize):
		return h.jsonError(c, 400, "chunk size must match the advertised server part size, except for the last chunk")
	default:
		h.logger.ErrorContext(c, "append tus upload", logger.Error(err))
		return c.SendStatus(http.StatusInternalServerError)
	}
}

func (h *Handler) TusComplete(c fiber.Ctx) error {
	setTusHeaders(c)
	if !isTusResumable(c) {
		return c.SendStatus(http.StatusPreconditionFailed)
	}
	session, err := h.loadTus(c)
	if err != nil {
		return h.writeRequestError(c, err)
	}
	actor, err := h.contextBuilder(reqCtx(c), session.Metadata)
	if err != nil {
		return h.jsonError(c, 401, "upload identity is invalid")
	}
	if !h.isTusOwner(actor, session) {
		return c.SendStatus(http.StatusForbidden)
	}
	if session.Offset != session.UploadLength {
		return h.jsonError(c, 409, "upload is not complete")
	}
	objectType, objectID, err := h.parseTusObject(session.Metadata)
	if err != nil {
		return h.jsonError(c, 400, err.Error())
	}
	if err := h.authorize(reqCtx(c), actor, "upload", requestObject(objectType.String(), objectID.Int64())); err != nil {
		return h.writeRequestError(c, err)
	}
	resolution, err := h.resolveUploadStrategy(reqCtx(c),
		actor,
		objectType,
		objectID,
		normalizedContext(metadataValue(session.Metadata,
			"context")),
		model.GetFileTypeString(normalizedContext(metadataValue(session.Metadata,
			"file_type"))),
		parseBoolFlag(metadataValue(session.Metadata,
			"skip_resize")),
		resolveUploadStrategyOptions{
			checkCanUpload:   true,
			includeAfterJobs: true,
		})
	if err != nil {
		return h.writeStrategyError(c, err)
	}
	managerID, userID, err := h.actorIDs(reqCtx(c), actor)
	if err != nil {
		return h.writeRequestError(c, err)
	}
	complete, err := h.tusStore.Complete(reqCtx(c), session.ID)
	if err != nil {
		if errors.Is(err,
			tusupload.ErrOffsetMismatch) || errors.Is(err,
			tusupload.ErrUploadBusy) || errors.Is(err,
			tusupload.ErrFenceLost) {
			return h.jsonError(c, 409, "upload finalization is not ready")
		}
		return h.jsonError(c, 500, "failed to finalize upload")
	}
	if complete.FinalizationKey == "" {
		if complete.Reader != nil {
			_ = complete.Reader.Close()
		}
		return h.jsonError(c, 500, "TUS store must provide a durable finalization key")
	}
	req := uploadservice.ReaderRequest{
		UploaderUUID: actor.UserUUID,
		ManagerID:    managerID,
		UserID:       userID,
		ObjectType:   objectType,
		ObjectID:     objectID,
		DeletedID: fileshared.FileObjectID(parseInt64(metadataValue(session.Metadata,
			"replace_file_id",
			"deleteId",
			"delete_id"))),
		AfterJobs:       resolution.afterJobs,
		Config:          resolution.config,
		FinalizationKey: complete.FinalizationKey,
	}
	file, err := h.uploadCompletedTus(c, req, complete)
	if err != nil {
		if errors.Is(err, model.ErrFinalizationGone) {
			return h.jsonError(c, 410, "completed upload is no longer available")
		}
		if errors.Is(err, model.ErrFinalizationConflict) {
			return h.jsonError(c, 409, "upload finalization conflicts with its original binding")
		}
		h.logger.ErrorContext(c, "finalize tus upload", logger.Error(err))
		return h.writeRequestError(c, err)
	}
	// Keep the ready protocol session until its TTL: retries must return the same
	// FileID. S3 cleanup consults the durable handoff before deleting any source.
	return c.Status(http.StatusAccepted).JSON(uploadFileResponse{File: h.mapFile(file)})
}

func (h *Handler) uploadCompletedTus(
	c fiber.Ctx,
	req uploadservice.ReaderRequest,
	complete tusupload.CompleteResult,
) (model.File, error) {
	if complete.Reader != nil {
		defer complete.Reader.Close()
		return h.taskUploader.UploadReader(reqCtx(c),
			req,
			uploadservice.ReaderUploadInput{
				OriginalName: complete.OriginalName,
				Size:         complete.Size,
				Reader:       complete.Reader,
			})
	}
	uploaded, err := uploadedFileFromCompleteResult(complete)
	if err != nil {
		return model.File{}, err
	}
	return h.taskUploader.UploadStored(reqCtx(c), req, uploaded)
}

func uploadedFileFromCompleteResult(complete tusupload.CompleteResult) (uploadservice.UploadedFile, error) {
	storagePath, err := filesanitize.EnsureRelativePath(complete.RelativePath)
	if err != nil {
		return uploadservice.UploadedFile{}, fmt.Errorf("normalize completed tus path: %w", err)
	}
	return uploadservice.UploadedFile{
		OriginalName: complete.OriginalName,
		FileName:     path.Base(storagePath),
		Size:         complete.Size,
		Path:         storagePath,
		FolderPath:   filesanitize.EnsureRelativeDir(storagePath),
		URL:          complete.URL,
		MimeType:     complete.MimeType,
		Width:        complete.Width,
		Height:       complete.Height,
	}, nil
}

func (h *Handler) Upload(c fiber.Ctx) error {
	objectType, objectID, err := h.getObjectRequest(c)
	if err != nil {
		return h.jsonError(c, 400, "entity_type is required")
	}
	if objectID <= 0 {
		return h.jsonError(c, 400, "entity_id is required")
	}
	contextName := normalizedContext(c.FormValue("context"))
	fileType := model.GetFileTypeString(normalizedContext(c.FormValue("file_type")))
	metadata := map[string]string{
		"entity_type": objectType.String(),
		"entity_id":   objectID.String(),
		"context":     contextName,
		"file_type":   c.FormValue("file_type"),
	}
	actor, err := h.contextBuilder(reqCtx(c), metadata)
	if err != nil {
		return h.jsonError(c, 401, "upload identity is invalid")
	}
	if err := h.authorize(reqCtx(c), actor, "upload", requestObject(objectType.String(), objectID.Int64())); err != nil {
		return h.writeRequestError(c, err)
	}
	resolution, err := h.resolveUploadStrategy(reqCtx(c),
		actor,
		objectType,
		objectID,
		contextName,
		fileType,
		parseBoolFlag(c.FormValue("skip_resize")),
		resolveUploadStrategyOptions{
			checkCanUpload:   true,
			includeAfterJobs: true,
		})
	if err != nil {
		return h.writeStrategyError(c, err)
	}
	managerID, userID, err := h.actorIDs(reqCtx(c), actor)
	if err != nil {
		return h.writeRequestError(c, err)
	}
	req := uploadservice.SingleRequest{
		UploaderUUID: actor.UserUUID,
		ManagerID:    managerID,
		UserID:       userID,
		ObjectType:   objectType,
		ObjectID:     objectID,
		DeletedID:    fileshared.FileObjectID(h.getDeletedIDRequest(c)),
		AfterJobs:    resolution.afterJobs,
		Config:       resolution.config,
	}
	if header, getErr := c.FormFile("file"); getErr == nil && header != nil {
		req.FileHeader = header
		return h.uploadSingleFile(c, req)
	}
	form, err := c.MultipartForm()
	if err != nil {
		return h.jsonErrorWithDetail(c, 400, "invalid multipart payload", err)
	}
	headers := form.File["files"]
	if len(headers) == 0 {
		return h.jsonError(c, 400, "file is required")
	}
	files, err := h.taskUploader.UploadBatch(reqCtx(c),
		uploadservice.BatchRequest{
			UploaderUUID: req.UploaderUUID,
			ManagerID:    req.ManagerID,
			UserID:       req.UserID,
			FileHeaders:  headers,
			ObjectType:   req.ObjectType,
			ObjectID:     req.ObjectID,
			DeletedID:    req.DeletedID,
			AfterJobs:    req.AfterJobs,
			Config:       req.Config,
		})
	if err != nil {
		var batchErr *uploadservice.BatchError
		if errors.As(err, &batchErr) && len(files) > 0 {
			// Accepted files must remain visible to callers even when a later item
			// fails. The successful prefix has already been committed to the outbox.
			return c.Status(http.StatusMultiStatus).JSON(uploadListResponse{
				Files:       h.mapFiles(files),
				FailedIndex: &batchErr.FailedIndex,
				Error:       publicUploadError(batchErr.Err),
			})
		}
		if errors.Is(err, uploadservice.ErrNoFiles) {
			return h.jsonError(c, 400, "file is required")
		}
		return h.writeRequestError(c, err)
	}
	return c.Status(http.StatusAccepted).JSON(uploadListResponse{Files: h.mapFiles(files)})
}

func publicUploadError(err error) string {
	var clientErr uploadservice.ClientError
	if errors.As(err, &clientErr) {
		return clientErr.Message
	}
	return "file was not accepted"
}

func (h *Handler) uploadSingleFile(c fiber.Ctx, req uploadservice.SingleRequest) error {
	file, err := h.taskUploader.UploadSingle(reqCtx(c), req)
	if err != nil {
		h.logger.ErrorContext(c, "upload file", logger.Error(err))
		return h.writeRequestError(c, err)
	}
	return c.Status(http.StatusAccepted).JSON(uploadFileResponse{File: h.mapFile(file)})
}

func (h *Handler) ListFiles(c fiber.Ctx) error {
	objectType := fileshared.FileObjectType(c.Query("entity_type"))
	if objectType.Validate() != nil {
		return h.jsonError(c, 400, "entity_type is required")
	}
	objectID, err := strconv.ParseInt(c.Query("entity_id"), 10, 64)
	if err != nil || objectID <= 0 {
		return h.jsonError(c, 400, "entity_id is required")
	}
	actor, err := h.contextBuilder(reqCtx(c),
		map[string]string{
			"entity_type": objectType.String(),
			"entity_id": strconv.FormatInt(objectID,
				10),
		})
	if err != nil {
		return h.jsonError(c, 401, "upload identity is invalid")
	}
	if err := h.authorize(reqCtx(c), actor, "list", requestObject(objectType.String(), objectID)); err != nil {
		return h.writeRequestError(c, err)
	}
	limit, offset := 50, 0
	if raw := c.Query("limit"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value <= 0 {
			return h.jsonError(c, 400, "invalid limit")
		}
		limit = min(value, filerepo.MaxListLimit)
	}
	if raw := c.Query("offset"); raw != "" {
		value, parseErr := strconv.Atoi(raw)
		if parseErr != nil || value < 0 {
			return h.jsonError(c, 400, "invalid offset")
		}
		offset = value
	}
	filters := filerepo.ListFilters{
		ObjectType: objectType.String(),
		ObjectID:   objectID,
		Limit:      limit,
		Offset:     offset,
		SkipTotal:  true,
	}
	if raw := normalizedContext(c.Query("file_type")); raw != "" {
		if model.GetFileTypeString(raw) == model.FileTypeUnknown {
			return h.jsonError(c, 400, "invalid file type")
		}
		filters.FileType = raw
	}
	files, _, err := h.fileRepo.List(reqCtx(c), filters)
	if err != nil {
		h.logger.ErrorContext(c, "list files", logger.Error(err))
		return h.jsonError(c, 500, "failed to list files")
	}
	items := h.mapFiles(files)
	if items == nil {
		items = []fileResponse{}
	}
	return c.JSON(listResponse{Files: items, EntityID: objectID})
}

func (h *Handler) GetFile(c fiber.Ctx) error {
	fileID, err := strconv.ParseInt(c.Params("id"), 10, 64)
	if err != nil || fileID <= 0 {
		return h.jsonError(c, 400, "invalid task id")
	}
	actor, err := h.contextBuilder(reqCtx(c), nil)
	if err != nil {
		return h.jsonError(c, 401, "upload identity is invalid")
	}
	file, err := h.taskUploader.GetFile(reqCtx(c), fileID)
	if err != nil {
		if errors.Is(err, uploadservice.ErrTaskNotFound) || errors.Is(err, uploadservice.ErrFileNotFound) {
			return h.jsonError(c, 404, "task not found")
		}
		return h.jsonError(c, 500, "failed to load task")
	}
	if err := h.authorize(reqCtx(c), actor, "read", file); err != nil {
		return h.writeRequestError(c, err)
	}
	return c.JSON(uploadFileResponse{File: h.mapFile(file)})
}

func (h *Handler) DeleteFile(c fiber.Ctx) error {
	fileID, err := h.parseFileID(c)
	if err != nil {
		return h.jsonError(c, 400, err.Error())
	}
	if !h.isConfirmed(c) {
		return h.jsonError(c, 400, "confirmation required")
	}
	actor, err := h.contextBuilder(reqCtx(c), nil)
	if err != nil {
		return h.jsonError(c, 401, "upload identity is invalid")
	}
	file, err := h.fileRepo.GetByID(reqCtx(c), fileID)
	if err != nil {
		return h.jsonError(c, 500, "failed to load file")
	}
	if file.ID == 0 {
		return h.jsonError(c, 404, "file not found")
	}
	if err := h.authorize(reqCtx(c), actor, "delete", file); err != nil {
		return h.writeRequestError(c, err)
	}
	if err := h.taskUploader.DeleteFile(
		reqCtx(c),
		uploadservice.DeleteRequest{UserRequestID: actor.UserUUID, FileID: fileID},
	); err != nil {
		h.logger.ErrorContext(c, "delete file", logger.Error(err))
		return h.jsonError(c, 500, "failed to enqueue delete task")
	}
	return c.Status(http.StatusAccepted).JSON(deleteResponse{Status: deleteStatusPending, ID: file.ID, EntityID: fileObjectID(file)})
}

func setTusHeaders(c fiber.Ctx) {
	c.Set("Tus-Resumable", tusupload.Version)
	c.Set("Access-Control-Expose-Headers", tusupload.ExposeHeaders+", Upload-Chunk-Size")
	c.Set("Cache-Control", "no-store")
}

func isTusResumable(c fiber.Ctx) bool {
	return strings.TrimSpace(c.Get("Tus-Resumable")) == tusupload.Version
}

func (h *Handler) isTusOwner(actor uploadstrategies.UploadContext, session tusupload.Session) bool {
	if !session.OwnerUUID.IsZero() {
		return !actor.UserUUID.IsZero() && session.OwnerUUID == actor.UserUUID
	}
	if session.OwnerID > 0 {
		return actor.UserID > 0 && session.OwnerID == actor.UserID
	}
	return h.policy.AllowAnonymousTUS
}

func (h *Handler) parseFileID(c fiber.Ctx) (int64, error) {
	raw := c.Params("id")
	if raw == "" {
		raw = c.FormValue("fileId")
	}
	if raw == "" {
		return 0, errors.New("file id is required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, errors.New("invalid file id")
	}
	return id, nil
}

func (h *Handler) isConfirmed(c fiber.Ctx) bool {
	return strings.EqualFold(c.Query("confirm"), "true") || strings.EqualFold(c.FormValue("confirm"), "true")
}

func (h *Handler) resolveConfig(objectType fileshared.FileObjectType,
	objectID fileshared.FileObjectID,
	_ string,
	fileType model.FileType,
) *uploadservice.FileUploadConfig {
	config := uploadservice.DefaultFileUploadConfig(objectType.String(), objectID.String())
	//nolint:exhaustive // Other file kinds retain the explicit default allowlist.
	switch fileType {
	case model.FileTypeVideo:
		config.MaxFileSize = 50 * 1024 * 1024
		config.AllowedExtensions = []string{".mp4", ".webm"}
		config.AllowedMimeTypes = map[string][]string{".mp4": {"video/mp4"}, ".webm": {"video/webm"}}
	case model.FileTypeImage:
		config.AllowedExtensions = []string{".jpg", ".jpeg", ".png", ".gif", ".webp"}
		config.AllowedMimeTypes = map[string][]string{
			".jpg":  {"image/jpeg"},
			".jpeg": {"image/jpeg"},
			".png":  {"image/png"},
			".gif":  {"image/gif"},
			".webp": {"image/webp"},
		}
	}
	return config
}

type (
	resolveUploadStrategyOptions   struct{ checkCanUpload, includeAfterJobs bool }
	resolveUploadStrategyErrorKind string
)

const (
	resolveUploadStrategyErrorForbidden resolveUploadStrategyErrorKind = "forbidden"
	resolveUploadStrategyErrorInternal  resolveUploadStrategyErrorKind = "internal"
)

type resolveUploadStrategyError struct {
	kind resolveUploadStrategyErrorKind
	err  error
}

func (e resolveUploadStrategyError) Error() string { return e.err.Error() }
func (e resolveUploadStrategyError) Unwrap() error { return e.err }

type resolveUploadStrategyResult struct {
	config    *uploadservice.FileUploadConfig
	afterJobs []fileshared.FileEventAfterJob
}

func (h *Handler) resolveUploadStrategy(
	ctx context.Context,
	actor uploadstrategies.UploadContext,
	objectType fileshared.FileObjectType,
	objectID fileshared.FileObjectID,
	contextValue string,
	fileType model.FileType,
	skipResize bool,
	options resolveUploadStrategyOptions,
) (resolveUploadStrategyResult, error) {
	name := normalizedContext(contextValue)
	strategy := h.getStrategy(name)
	if strategy == nil && (name != "" || !h.policy.AllowDefaultStrategy) {
		return resolveUploadStrategyResult{},
			resolveUploadStrategyError{
				resolveUploadStrategyErrorForbidden,
				errors.New("upload context is not configured"),
			}
	}
	if strategy != nil && options.checkCanUpload {
		if err := strategy.CanUpload(ctx, actor); err != nil {
			return resolveUploadStrategyResult{}, resolveUploadStrategyError{resolveUploadStrategyErrorForbidden, err}
		}
	}
	var config *uploadservice.FileUploadConfig
	var jobs []fileshared.FileEventAfterJob
	if strategy == nil {
		config = h.resolveConfig(objectType, objectID, name, fileType)
	} else {
		config = strategy.GetConfig(ctx, actor)
		if options.includeAfterJobs {
			var err error
			jobs, err = strategy.GetAfterJobs(ctx, actor)
			if err != nil {
				return resolveUploadStrategyResult{}, resolveUploadStrategyError{resolveUploadStrategyErrorInternal, err}
			}
		}
	}
	if config == nil {
		return resolveUploadStrategyResult{},
			resolveUploadStrategyError{
				resolveUploadStrategyErrorInternal,
				fmt.Errorf("upload strategy %q returned nil config",
					name),
			}
	}
	if config.MaxFileSize < 0 {
		return resolveUploadStrategyResult{},
			resolveUploadStrategyError{
				resolveUploadStrategyErrorInternal,
				errors.New("upload size limit must not be negative"),
			}
	}
	config = uploadservice.ResolveFileUploadConfig(config)
	if skipResize {
		config.SkipResizer = true
	}
	return resolveUploadStrategyResult{config: config, afterJobs: append([]fileshared.FileEventAfterJob(nil), jobs...)}, nil
}

func (h *Handler) writeStrategyError(c fiber.Ctx, err error) error {
	var strategyErr resolveUploadStrategyError
	if errors.As(err, &strategyErr) && strategyErr.kind == resolveUploadStrategyErrorForbidden {
		return h.jsonError(c, 403, strategyErr.Error())
	}
	h.logger.ErrorContext(c, "resolve upload strategy", logger.Error(err))
	return h.jsonError(c, 500, "failed to process upload")
}

func (h *Handler) getObjectRequest(c fiber.Ctx) (fileshared.FileObjectType, fileshared.FileObjectID, error) {
	raw := c.FormValue("objectType")
	if value := c.FormValue("entity_type"); value != "" {
		raw = value
	}
	kind := fileshared.FileObjectType(raw)
	if err := kind.Validate(); err != nil {
		return kind, 0, err
	}
	raw = c.FormValue("objectId")
	if value := c.FormValue("entity_id"); value != "" {
		raw = value
	}
	return kind, fileshared.FileObjectID(parseInt64(raw)), nil
}

func (h *Handler) getDeletedIDRequest(c fiber.Ctx) int64 {
	raw := c.FormValue("deleteId")
	if value := c.FormValue("replace_file_id"); value != "" {
		raw = value
	}
	return parseInt64(raw)
}

func parseBoolFlag(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "y", "on":
		return true
	default:
		return false
	}
}

func parseTusLength(raw string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("invalid Upload-Length")
	}
	return value, nil
}

func parseTusOffset(raw string) (int64, error) {
	value, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || value < 0 {
		return 0, errors.New("invalid Upload-Offset")
	}
	return value, nil
}

func parseTusMetadata(raw string) (map[string]string, error) {
	if len(raw) > 16*1024 {
		return nil, errors.New("upload metadata exceeds 16 KiB")
	}
	result := make(map[string]string)
	for _, pair := range strings.Split(raw, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, " ", 2)
		key := parts[0]
		if _, exists := result[key]; exists {
			return nil, fmt.Errorf("duplicate upload metadata key %q", key)
		}
		if len(result) >= 64 {
			return nil, errors.New("too many upload metadata fields")
		}
		if len(parts) == 1 {
			result[key] = ""
			continue
		}
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(parts[1]))
		if err != nil {
			return nil, fmt.Errorf("decode tus metadata %q: %w", key, err)
		}
		result[key] = string(decoded)
	}
	return result, nil
}

func metadataValue(metadata map[string]string, keys ...string) string {
	for _, key := range keys {
		if value := metadata[key]; strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (h *Handler) parseTusObject(metadata map[string]string) (fileshared.FileObjectType, fileshared.FileObjectID, error) {
	kind := fileshared.FileObjectType(metadataValue(metadata, "entity_type", "object_type", "objectType"))
	if kind.Validate() != nil {
		return kind, 0, errors.New("entity_type is required")
	}
	raw := metadataValue(metadata, "entity_id", "object_id", "objectId")
	if raw == "" {
		return kind, 0, errors.New("entity_id is required")
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return kind, 0, errors.New("invalid entity_id")
	}
	return kind, fileshared.FileObjectID(id), nil
}

func (h *Handler) resolveTusPatchMimeType(
	c fiber.Ctx,
	session tusupload.Session,
	body []byte,
	offset int64,
	cmsOnly bool,
) (string, error) {
	contextValue := normalizedContext(metadataValue(session.Metadata, "context"))
	var kind fileshared.FileObjectType
	var id fileshared.FileObjectID
	var err error
	if cmsOnly {
		if contextValue != "cms" || h.getStrategy(contextValue) == nil {
			return "", reject(403, "CMS upload context is not configured")
		}
	} else {
		kind, id, err = h.parseTusObject(session.Metadata)
		if err != nil {
			return "", reject(400, err.Error())
		}
	}
	actor, err := h.contextBuilder(reqCtx(c), session.Metadata)
	if err != nil {
		return "", reject(401, "upload identity is invalid")
	}
	resolution, err := h.resolveUploadStrategy(reqCtx(c),
		actor,
		kind,
		id,
		contextValue,
		model.GetFileTypeString(normalizedContext(metadataValue(session.Metadata,
			"file_type"))),
		parseBoolFlag(metadataValue(session.Metadata,
			"skip_resize")),
		resolveUploadStrategyOptions{checkCanUpload: true})
	if err != nil {
		var strategyErr resolveUploadStrategyError
		if errors.As(err, &strategyErr) && strategyErr.kind == resolveUploadStrategyErrorForbidden {
			return "", reject(403, "upload is no longer permitted")
		}
		return "", reject(500, "failed to process upload")
	}
	if session.UploadLength > resolution.config.MaxFileSize || session.UploadLength > filepolicy.MaxFileSize {
		return "", reject(http.StatusRequestEntityTooLarge, "file is too large")
	}
	ext := strings.ToLower(filepath.Ext(strings.TrimSpace(metadataValue(session.Metadata, "filename", "file_name", "fileName"))))
	if session.MimeType != "" {
		if !isAllowedMimeType(ext, session.MimeType, resolution.config.AllowedMimeTypes) {
			return "", reject(400, "file mime type is not allowed")
		}
		return "", nil
	}
	prefixReader, canReadPrefix := h.tusStore.(interface {
		ReadPrefix(ctx context.Context, id string, offset int64) ([]byte, error)
	})
	if offset != 0 && !canReadPrefix {
		return "", nil
	}
	var sniff []byte
	if offset > 0 {
		sniff, err = prefixReader.ReadPrefix(reqCtx(c), session.ID, offset)
		if err != nil {
			return "", reject(409, "upload offset changed")
		}
	}
	if len(sniff) > tusupload.SniffLen {
		sniff = sniff[:tusupload.SniffLen]
	}
	sniff = append(sniff, body[:min(len(body), tusupload.SniffLen-len(sniff))]...)
	mimeType := http.DetectContentType(sniff)
	if !isAllowedMimeType(ext, mimeType, resolution.config.AllowedMimeTypes) {
		if canReadPrefix && offset+int64(len(body)) < session.UploadLength &&
			hasIncompleteMIMEHeader(sniff, resolution.config.AllowedMimeTypes[ext]) {
			return "", nil // Still private and unapproved; inspect the next PATCH.
		}
		return "", reject(400, "file mime type is not allowed")
	}
	return mimeType, nil
}

func hasIncompleteMIMEHeader(sniff []byte, allowed []string) bool {
	for _, candidate := range allowed {
		if filepolicy.IncompleteMIMEHeader(sniff, candidate) {
			return true
		}
	}
	return false
}

func parseInt64(raw string) int64 {
	value, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return value
}

func isAllowedExtension(ext string, allowed []string) bool {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" {
		return false
	}
	if len(allowed) == 0 {
		return true
	}
	for _, value := range allowed {
		candidate := strings.ToLower(strings.TrimSpace(value))
		if !strings.HasPrefix(candidate, ".") {
			candidate = "." + candidate
		}
		if candidate == ext {
			return true
		}
	}
	return false
}

func isAllowedMimeType(ext, contentType string, allowed map[string][]string) bool {
	ext = strings.ToLower(strings.TrimSpace(ext))
	contentType = strings.ToLower(strings.TrimSpace(strings.SplitN(contentType, ";", 2)[0]))
	if contentType == "" {
		return false
	}
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed[ext] {
		candidate = strings.ToLower(strings.TrimSpace(strings.SplitN(candidate, ";", 2)[0]))
		if candidate == contentType {
			return true
		}
		if strings.HasSuffix(candidate, "/*") && strings.HasPrefix(contentType, strings.TrimSuffix(candidate, "*")) {
			return true
		}
	}
	return false
}

func fileObjectID(file model.File) int64 {
	if file.ObjectID == nil {
		return 0
	}
	return file.ObjectID.Int64()
}

func (h *Handler) mapFiles(files []model.File) []fileResponse {
	if len(files) == 0 {
		return nil
	}
	result := make([]fileResponse, 0, len(files))
	for _, file := range files {
		result = append(result, *h.mapFile(file))
	}
	return result
}

func (h *Handler) mapFile(file model.File) *fileResponse {
	status := file.GetData().Uploader.Status.String()
	if status == "" {
		status = fileshared.FileUploadTaskStatusCompleted.String()
	}
	url, publicURL, thumbnail, fullPath, folderPath := "", "", "", "", ""
	if status == fileshared.FileUploadTaskStatusCompleted.String() {
		publicURL = h.urlComposer(file.GetPublicURL())
		url = publicURL
		if file.URL != "" {
			url = h.urlComposer(file.URL)
		}
		thumbnail = file.PreferredPreviewPath()
		if thumbnail != "" {
			thumbnail = h.urlComposer(thumbnail)
		}
		fullPath = file.GetFullPath()
		folderPath = file.FolderPath
	}
	return &fileResponse{
		ID:           file.ID,
		EntityID:     fileObjectID(file),
		Filename:     file.FileName,
		OriginalName: file.OriginalFileName,
		FileType:     file.FileType.String(),
		MimeType:     file.MimeType,
		Size:         file.Size,
		URL:          url,
		PublicURL:    publicURL,
		ThumbnailURL: thumbnail,
		FullPath:     fullPath,
		FolderPath:   folderPath,
		SortOrder:    file.Position,
		Status:       status,
		IsPrimary:    file.IsPrimary,
		Width:        file.GetWidth(),
		Height:       file.GetHeight(),
		CreatedAt:    pointerTime(file.CreatedAt),
		UpdatedAt:    pointerTime(file.UpdatedAt),
		Data:         buildFileDataResponse(file),
	}
}

func pointerTime(value time.Time) *time.Time {
	if value.IsZero() {
		return nil
	}
	return &value
}

func buildFileDataResponse(file model.File) *fileDataResponse {
	data := file.GetData()
	if data == nil {
		return nil
	}
	response := &fileDataResponse{Width: data.Width, Height: data.Height, Alt: data.Alt}
	if data.Provider.Driver != "" {
		response.Provider = &fileProviderResponse{Driver: data.Provider.Driver}
	}
	return response
}

func (h *Handler) jsonError(c fiber.Ctx, status int, message string) error {
	return c.Status(status).JSON(errorResponse{Status: "error", Error: message})
}

func (h *Handler) jsonErrorWithDetail(c fiber.Ctx, status int, message string, err error) error {
	if err != nil {
		h.logger.DebugContext(c, "request validation failed", logger.Error(err))
	}
	return h.jsonError(c, status, message)
}

func (h *Handler) jsonValidationError(c fiber.Ctx, validationErrors map[string]string) error {
	return c.Status(http.StatusBadRequest).JSON(validationResponse{Status: "error", Errors: validationErrors})
}

type uploadListResponse struct {
	Files       []fileResponse `json:"files,omitempty"`
	FailedIndex *int           `json:"failedIndex,omitempty"`
	Error       string         `json:"error,omitempty"`
}
type uploadFileResponse struct {
	File *fileResponse `json:"file,omitempty"`
}
type fileResponse struct {
	ID           int64             `json:"id"`
	EntityID     int64             `json:"entityId"`
	Filename     string            `json:"filename"`
	OriginalName string            `json:"originalName"`
	FileType     string            `json:"fileType"`
	MimeType     string            `json:"mimeType,omitempty"`
	Size         int64             `json:"size"`
	URL          string            `json:"url"`
	PublicURL    string            `json:"publicUrl,omitempty"`
	ThumbnailURL string            `json:"thumbnailUrl,omitempty"`
	FullPath     string            `json:"fullPath,omitempty"`
	FolderPath   string            `json:"folderPath,omitempty"`
	SortOrder    int               `json:"sortOrder"`
	Status       string            `json:"status"`
	IsPrimary    bool              `json:"isPrimary"`
	Width        int               `json:"width,omitempty"`
	Height       int               `json:"height,omitempty"`
	CreatedAt    *time.Time        `json:"createdAt,omitempty"`
	UpdatedAt    *time.Time        `json:"updatedAt,omitempty"`
	Data         *fileDataResponse `json:"data,omitempty"`
}
type fileDataResponse struct {
	Provider *fileProviderResponse `json:"provider,omitempty"`
	Width    int                   `json:"width,omitempty"`
	Height   int                   `json:"height,omitempty"`
	Alt      string                `json:"alt,omitempty"`
}
type fileProviderResponse struct {
	Driver string `json:"driver,omitempty"`
}
type listResponse struct {
	Files    []fileResponse `json:"files"`
	EntityID int64          `json:"entityId"`
}
type deleteResponse struct {
	Status   string `json:"status"`
	ID       int64  `json:"id"`
	EntityID int64  `json:"entityId"`
}
type errorResponse struct {
	Status string `json:"status"`
	Error  string `json:"error"`
}
type validationResponse struct {
	Status string            `json:"status"`
	Errors map[string]string `json:"errors"`
}

const deleteStatusPending = "pending"

// TusChunkSize returns the configured chunk size of the underlying TUS store,
// or 0 if not defined or if the store does not implement ChunkSize().
func (h *Handler) TusChunkSize() int64 {
	if store, ok := h.tusStore.(interface{ ChunkSize() int64 }); ok {
		return store.ChunkSize()
	}
	return 0
}
