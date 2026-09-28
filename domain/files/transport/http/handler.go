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
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	fileshared "github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/internal/filesanitize"
	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

//go:generate toolsmocks

// TaskUploader exposes upload operations.
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

// ContextBuilder constructs the agnostic UploadContext from the fiber context (e.g. extracts UserID).
type ContextBuilder func(ctx context.Context, metadata map[string]string) (uploadstrategies.UploadContext, error)

// URLComposer transforms a raw file path/URL into a full public URL.
type URLComposer func(path string) string

// Handler is a generic upload handler.
type Handler struct {
	taskUploader   TaskUploader
	fileRepo       FileRepository
	tusStore       tusupload.Store
	logger         logger.Logger
	contextBuilder ContextBuilder
	urlComposer    URLComposer
	strategies     map[string]uploadstrategies.Strategy
}

// NewHandler creates a new generic upload handler.
func NewHandler(
	taskUploader TaskUploader,
	fileRepo FileRepository,
	tusStore tusupload.Store,
	logger logger.Logger,
	contextBuilder ContextBuilder,
	urlComposer URLComposer,
) *Handler {
	return &Handler{
		taskUploader:   taskUploader,
		fileRepo:       fileRepo,
		tusStore:       tusStore,
		logger:         logger,
		contextBuilder: contextBuilder,
		urlComposer:    urlComposer,
		strategies:     make(map[string]uploadstrategies.Strategy),
	}
}

// RegisterStrategy registers a custom upload strategy.
func (h *Handler) RegisterStrategy(contextName string, strategy uploadstrategies.Strategy) {
	h.strategies[strings.ToLower(contextName)] = strategy
}

// --- Handlers ---

func (h *Handler) TusOptions(c fiber.Ctx) error {
	setTusHeaders(c)
	c.Set("Tus-Version", tusupload.Version)
	c.Set("Tus-Extension", tusupload.Extension)
	c.Set("Access-Control-Allow-Methods", "OPTIONS, POST, HEAD, PATCH")
	c.Set("Access-Control-Allow-Headers", strings.Join([]string{
		"Tus-Resumable",
		"Upload-Length",
		"Upload-Offset",
		"Upload-Metadata",
		"Content-Type",
		"Content-Length",
		"X-CSRF-Token",
	}, ", "))

	return c.SendStatus(http.StatusNoContent)
}

func (h *Handler) TusCreate(c fiber.Ctx) error {
	return h.tusCreate(c, false)
}

func (h *Handler) TusCreateCMS(c fiber.Ctx) error {
	return h.tusCreate(c, true)
}

func (h *Handler) tusCreate(c fiber.Ctx, cmsOnly bool) error {
	setTusHeaders(c)

	if !isTusResumable(c) {
		return c.SendStatus(http.StatusPreconditionFailed)
	}

	if c.Get("Upload-Defer-Length") != "" {
		return h.jsonError(c, fiber.StatusBadRequest, "deferred length is not supported")
	}

	uploadLength, err := parseTusLength(c.Get("Upload-Length"))
	if err != nil {
		return h.jsonError(c, fiber.StatusBadRequest, "invalid upload length")
	}

	metadata, err := parseTusMetadata(c.Get("Upload-Metadata"))
	if err != nil {
		return h.jsonError(c, fiber.StatusBadRequest, "invalid upload metadata")
	}

	filename := strings.TrimSpace(metadataValue(metadata, "filename", "file_name", "fileName"))
	if filename == "" {
		return h.jsonError(c, fiber.StatusBadRequest, "filename is required")
	}

	// Strategy Check
	uCtx, err := h.contextBuilder(c, metadata)
	if err != nil {
		return h.jsonError(c, fiber.StatusUnauthorized, err.Error())
	}

	contextValue := strings.ToLower(strings.TrimSpace(metadataValue(metadata, "context")))

	var objectType fileshared.FileObjectType
	var objectID fileshared.FileObjectID
	if cmsOnly {
		if contextValue != "cms" {
			return h.jsonError(c, fiber.StatusBadRequest, "CMS upload context is required")
		}
		if h.getStrategy(contextValue) == nil {
			return h.jsonError(c, fiber.StatusInternalServerError, "CMS upload strategy is not configured")
		}
	} else {
		objectType, objectID, err = h.parseTusObject(metadata)
		if err != nil {
			return h.jsonError(c, fiber.StatusBadRequest, err.Error())
		}
	}

	fileTypeValue := strings.ToLower(strings.TrimSpace(metadataValue(metadata, "file_type")))
	fileType := model.GetFileTypeString(fileTypeValue)
	skipResize := parseBoolFlag(metadataValue(metadata, "skip_resize"))

	resolution, err := h.resolveUploadStrategy(
		c,
		uCtx,
		objectType,
		objectID,
		contextValue,
		fileType,
		skipResize,
		resolveUploadStrategyOptions{
			checkCanUpload: true,
		},
	)
	if err != nil {
		var resolveErr resolveUploadStrategyError
		if errors.As(err, &resolveErr) && resolveErr.kind == resolveUploadStrategyErrorForbidden {
			return h.jsonError(c, fiber.StatusForbidden, resolveErr.Error())
		}
		h.logger.ErrorContext(c, "failed to resolve upload strategy", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to process upload")
	}

	config := resolution.config

	if config.MaxFileSize > 0 && uploadLength > config.MaxFileSize {
		return h.jsonError(c, fiber.StatusRequestEntityTooLarge, "file is too large")
	}

	ext := strings.ToLower(filepath.Ext(filename))
	if !isAllowedExtension(ext, config.AllowedExtensions) {
		return h.jsonError(c, fiber.StatusBadRequest, "file extension is not allowed")
	}

	deleteID := parseInt64(metadataValue(metadata, "replace_file_id", "deleteId", "delete_id"))

	fileName := uuid.New().String() + ext
	sessionMetadata := map[string]string{
		"file_name":       fileName,
		"filename":        filename,
		"context":         contextValue,
		"file_type":       fileTypeValue,
		"skip_resize":     strconv.FormatBool(skipResize),
		"replace_file_id": strconv.FormatInt(deleteID, 10),
	}
	if !cmsOnly {
		sessionMetadata["entity_type"] = objectType.String()
		sessionMetadata["entity_id"] = objectID.String()
	}
	session, err := h.tusStore.Create(c, tusupload.CreateRequest{
		UploadLength: uploadLength,
		OriginalName: filename,
		FileName:     fileName,
		Metadata:     sessionMetadata,
		OwnerID:      uCtx.UserID,
		OwnerUUID:    uCtx.UserUUID,
	})
	if err != nil {
		h.logger.ErrorContext(c, "failed to create tus upload", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to create upload")
	}

	// Derive the resumable URL from the route that handled this request. One
	// handler can be mounted on generic and CMS-only prefixes concurrently.
	location := strings.TrimRight(c.Path(), "/") + "/" + session.ID
	c.Set("Location", location)

	return c.SendStatus(http.StatusCreated)
}

func (h *Handler) TusHead(c fiber.Ctx) error {
	setTusHeaders(c)

	if !isTusResumable(c) {
		return c.SendStatus(http.StatusPreconditionFailed)
	}

	session, err := h.tusStore.Get(c, c.Params("id"))
	if err != nil {
		if errors.Is(err, tusupload.ErrNotFound) {
			return c.SendStatus(http.StatusNotFound)
		}
		h.logger.ErrorContext(c, "failed to load tus upload", logger.Error(err))
		return c.SendStatus(http.StatusInternalServerError)
	}

	uCtx, _ := h.contextBuilder(c, nil)
	if !h.isTusOwner(uCtx, session) {
		return c.SendStatus(http.StatusForbidden)
	}

	c.Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
	c.Set("Upload-Length", strconv.FormatInt(session.UploadLength, 10))

	return c.SendStatus(http.StatusOK)
}

func (h *Handler) TusPatch(c fiber.Ctx) error {
	return h.tusPatch(c, false)
}

func (h *Handler) TusPatchCMS(c fiber.Ctx) error {
	return h.tusPatch(c, true)
}

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
		return h.jsonError(c, fiber.StatusBadRequest, "invalid upload offset")
	}

	session, err := h.tusStore.Get(c, c.Params("id"))
	if err != nil {
		if errors.Is(err, tusupload.ErrNotFound) {
			return c.SendStatus(http.StatusNotFound)
		}
		h.logger.ErrorContext(c, "failed to load tus upload", logger.Error(err))
		return c.SendStatus(http.StatusInternalServerError)
	}

	uCtx, _ := h.contextBuilder(c, nil)
	if !h.isTusOwner(uCtx, session) {
		return c.SendStatus(http.StatusForbidden)
	}

	if offset != session.Offset {
		c.Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
		return c.SendStatus(http.StatusConflict)
	}

	body := c.Body()
	if session.UploadLength >= 0 && offset+int64(len(body)) > session.UploadLength {
		c.Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
		return c.SendStatus(http.StatusRequestEntityTooLarge)
	}

	mimeType, err := h.resolveTusPatchMimeType(c, session, body, offset, cmsOnly)
	if err != nil {
		return err
	}

	newOffset, err := h.tusStore.Append(c, session.ID, offset, body, mimeType)
	if err != nil {
		return h.handleTusAppendError(c, session, err)
	}

	c.Set("Upload-Offset", strconv.FormatInt(newOffset, 10))
	return c.SendStatus(http.StatusNoContent)
}

func (h *Handler) handleTusAppendError(c fiber.Ctx, session tusupload.Session, err error) error {
	switch {
	case errors.Is(err, tusupload.ErrOffsetMismatch),
		errors.Is(err, tusupload.ErrUploadBusy),
		errors.Is(err, tusupload.ErrFenceLost):
		currentOffset := session.Offset
		if current, getErr := h.tusStore.Get(c, session.ID); getErr == nil {
			currentOffset = current.Offset
		}
		c.Set("Upload-Offset", strconv.FormatInt(currentOffset, 10))
		return c.SendStatus(http.StatusConflict)
	case errors.Is(err, tusupload.ErrLengthExceeded):
		c.Set("Upload-Offset", strconv.FormatInt(session.Offset, 10))
		return c.SendStatus(http.StatusRequestEntityTooLarge)
	case errors.Is(err, tusupload.ErrChunkTooSmall):
		return h.jsonError(c, fiber.StatusBadRequest, "chunk size too small")
	case errors.Is(err, tusupload.ErrChunkSize):
		return h.jsonError(c, fiber.StatusBadRequest, "intermediate chunk size must match the server part size")
	default:
		h.logger.ErrorContext(c, "failed to append tus upload", logger.Error(err))
		return c.SendStatus(http.StatusInternalServerError)
	}
}

func (h *Handler) TusComplete(c fiber.Ctx) error {
	setTusHeaders(c)

	if !isTusResumable(c) {
		return c.SendStatus(http.StatusPreconditionFailed)
	}

	session, err := h.tusStore.Get(c, c.Params("id"))
	if err != nil {
		if errors.Is(err, tusupload.ErrNotFound) {
			return c.SendStatus(http.StatusNotFound)
		}
		h.logger.ErrorContext(c, "failed to load tus upload", logger.Error(err))
		return c.SendStatus(http.StatusInternalServerError)
	}

	uCtx, err := h.contextBuilder(c, session.Metadata)
	if err != nil {
		return h.jsonError(c, fiber.StatusUnauthorized, err.Error())
	}

	if !h.isTusOwner(uCtx, session) {
		return c.SendStatus(http.StatusForbidden)
	}

	if session.UploadLength >= 0 && session.Offset < session.UploadLength {
		return h.jsonError(c, fiber.StatusConflict, "upload is not complete")
	}

	metadata := session.Metadata
	objectType, objectID, err := h.parseTusObject(metadata)
	if err != nil {
		return h.jsonError(c, fiber.StatusBadRequest, err.Error())
	}

	contextValue := strings.ToLower(strings.TrimSpace(metadataValue(metadata, "context")))
	fileType := model.GetFileTypeString(strings.ToLower(strings.TrimSpace(metadataValue(metadata, "file_type"))))
	skipResize := parseBoolFlag(metadataValue(metadata, "skip_resize"))
	deletedID := parseInt64(metadataValue(metadata, "replace_file_id", "deleteId", "delete_id"))

	resolution, err := h.resolveUploadStrategy(
		c,
		uCtx,
		objectType,
		objectID,
		contextValue,
		fileType,
		skipResize,
		resolveUploadStrategyOptions{
			includeAfterJobs: true,
		},
	)
	if err != nil {
		var resolveErr resolveUploadStrategyError
		if errors.As(err, &resolveErr) {
			if resolveErr.kind == resolveUploadStrategyErrorForbidden {
				return h.jsonError(c, fiber.StatusForbidden, resolveErr.Error())
			}
			h.logger.ErrorContext(c, "failed to get strategy after jobs", logger.Error(err))
			return h.jsonError(c, fiber.StatusInternalServerError, "failed to process upload")
		}
		h.logger.ErrorContext(c, "failed to resolve upload strategy", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to process upload")
	}

	completeResult, err := h.tusStore.Complete(c, session.ID)
	if err != nil {
		if errors.Is(err, tusupload.ErrOffsetMismatch) {
			return h.jsonError(c, fiber.StatusConflict, "upload is not complete")
		}
		if errors.Is(err, tusupload.ErrUploadBusy) || errors.Is(err, tusupload.ErrFenceLost) {
			return h.jsonError(c, fiber.StatusConflict, "upload finalization is already in progress")
		}
		h.logger.ErrorContext(c, "failed to finalize tus upload", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to finalize upload")
	}

	req := uploadservice.ReaderRequest{
		UploaderUUID: uCtx.UserUUID,
		ManagerID:    uCtx.UserID, // Using ID as ManagerID (admin)
		UserID:       0,           // Assuming user upload is 0 if admin
		ObjectType:   objectType,
		ObjectID:     objectID,
		DeletedID:    fileshared.FileObjectID(deletedID),
		Config:       resolution.config,
	}
	req.AfterJobs = append(req.AfterJobs, resolution.afterJobs...)

	fileModel, err := h.uploadCompletedTus(c, req, completeResult)
	if err != nil {
		var clientErr uploadservice.ClientError
		if errors.As(err, &clientErr) {
			return h.jsonError(c, fiber.StatusBadRequest, clientErr.Message)
		}

		var valErr uploadservice.ValidationError
		if errors.As(err, &valErr) {
			return h.jsonValidationError(c, valErr.Errors)
		}

		h.logger.ErrorContext(c, "failed to finalize tus upload", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to enqueue upload task")
	}

	response := uploadFileResponse{
		File: h.mapFile(fileModel),
	}

	if err := h.tusStore.Delete(c, session.ID); err != nil && !errors.Is(err, tusupload.ErrNotFound) {
		h.logger.WarnContext(c, "failed to cleanup tus upload", logger.Error(err))
	}

	return c.Status(http.StatusAccepted).JSON(response)
}

func (h *Handler) uploadCompletedTus(
	c fiber.Ctx,
	req uploadservice.ReaderRequest,
	complete tusupload.CompleteResult,
) (model.File, error) {
	if complete.Reader != nil {
		defer complete.Reader.Close()

		return h.taskUploader.UploadReader(c, req, uploadservice.ReaderUploadInput{
			OriginalName: complete.OriginalName,
			Size:         complete.Size,
			Reader:       complete.Reader,
		})
	}

	uploaded, err := uploadedFileFromCompleteResult(complete)
	if err != nil {
		h.logger.ErrorContext(c, "failed to map completed tus upload", logger.Error(err))

		return model.File{}, err
	}

	return h.taskUploader.UploadStored(c, req, uploaded)
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
		return h.jsonError(c, fiber.StatusBadRequest, "entity_type is required")
	}

	deletedID := h.getDeletedIDRequest(c)
	contextValue := strings.ToLower(strings.TrimSpace(c.FormValue("context")))
	fileType := model.GetFileTypeString(strings.ToLower(strings.TrimSpace(c.FormValue("file_type"))))
	skipResizer := parseBoolFlag(c.FormValue("skip_resize"))

	metadata := map[string]string{
		"entity_type": objectType.String(),
		"entity_id":   objectID.String(),
		"context":     contextValue,
		"file_type":   c.FormValue("file_type"),
	}

	uCtx, err := h.contextBuilder(c, metadata)
	if err != nil {
		return h.jsonError(c, fiber.StatusUnauthorized, err.Error())
	}

	if objectID <= 0 {
		return h.jsonError(c, fiber.StatusBadRequest, "entity_id is required")
	}

	resolution, err := h.resolveUploadStrategy(
		c,
		uCtx,
		objectType,
		objectID,
		contextValue,
		fileType,
		skipResizer,
		resolveUploadStrategyOptions{
			checkCanUpload:   true,
			includeAfterJobs: true,
		},
	)
	if err != nil {
		var resolveErr resolveUploadStrategyError
		if errors.As(err, &resolveErr) {
			if resolveErr.kind == resolveUploadStrategyErrorForbidden {
				return h.jsonError(c, fiber.StatusForbidden, resolveErr.Error())
			}
			h.logger.ErrorContext(c, "failed to get strategy after jobs", logger.Error(err))
			return h.jsonError(c, fiber.StatusInternalServerError, "failed to process upload")
		}
		h.logger.ErrorContext(c, "failed to resolve upload strategy", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to process upload")
	}

	singleReq := uploadservice.SingleRequest{
		UploaderUUID: uCtx.UserUUID,
		ManagerID:    uCtx.UserID,
		UserID:       0,
		ObjectType:   objectType,
		ObjectID:     objectID,
		DeletedID:    fileshared.FileObjectID(deletedID),
		Config:       resolution.config,
		AfterJobs:    resolution.afterJobs,
	}

	if fileHeader, err := c.FormFile("file"); err == nil && fileHeader != nil {
		singleReq.FileHeader = fileHeader
		return h.uploadSingleFile(c, singleReq)
	}

	form, err := c.MultipartForm()
	if err != nil {
		return h.jsonErrorWithDetail(c, fiber.StatusBadRequest, "invalid multipart payload", err)
	}

	fileHeaders := form.File["files"]
	if len(fileHeaders) == 0 {
		return h.jsonError(c, fiber.StatusBadRequest, "file is required")
	}

	batchReq := uploadservice.BatchRequest{
		UploaderUUID: uCtx.UserUUID,
		ManagerID:    uCtx.UserID,
		UserID:       0,
		FileHeaders:  fileHeaders,
		ObjectType:   objectType,
		ObjectID:     objectID,
		DeletedID:    fileshared.FileObjectID(deletedID),
		Config:       resolution.config,
		AfterJobs:    resolution.afterJobs,
	}

	files, err := h.taskUploader.UploadBatch(c, batchReq)
	if err != nil {
		switch {
		case errors.Is(err, uploadservice.ErrNoFiles):
			return h.jsonError(c, fiber.StatusBadRequest, "file is required")
		default:
			var valErr uploadservice.ValidationError
			if errors.As(err, &valErr) {
				return h.jsonValidationError(c, valErr.Errors)
			}

			h.logger.ErrorContext(c, "failed to upload batch", logger.Error(err))
			return h.jsonError(c, fiber.StatusInternalServerError, "failed to enqueue upload task")
		}
	}

	response := uploadListResponse{
		Files: h.mapFiles(files),
	}

	return c.Status(http.StatusAccepted).JSON(response)
}

func (h *Handler) uploadSingleFile(c fiber.Ctx, req uploadservice.SingleRequest) error {
	fileModel, err := h.taskUploader.UploadSingle(c, req)
	if err != nil {
		var clientErr uploadservice.ClientError
		if errors.As(err, &clientErr) {
			return h.jsonError(c, fiber.StatusBadRequest, clientErr.Message)
		}

		var valErr uploadservice.ValidationError
		if errors.As(err, &valErr) {
			return h.jsonValidationError(c, valErr.Errors)
		}

		h.logger.ErrorContext(c, "failed to upload file", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to enqueue upload task")
	}

	response := uploadFileResponse{
		File: h.mapFile(fileModel),
	}

	return c.Status(http.StatusAccepted).JSON(response)
}

// ListFiles returns files linked to entity.
func (h *Handler) ListFiles(c fiber.Ctx) error {
	objectType := fileshared.FileObjectType(c.Query("entity_type"))
	if err := objectType.Validate(); err != nil {
		return h.jsonError(c, fiber.StatusBadRequest, "entity_type is required")
	}

	entityID, err := strconv.ParseInt(c.Query("entity_id"), 10, 64)
	if err != nil || entityID <= 0 {
		return h.jsonError(c, fiber.StatusBadRequest, "entity_id is required")
	}

	limit := int64(50)
	if v := c.Query("limit"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed > 0 {
			limit = parsed
		}
	}

	offset := int64(0)
	if v := c.Query("offset"); v != "" {
		if parsed, err := strconv.ParseInt(v, 10, 64); err == nil && parsed >= 0 {
			offset = parsed
		}
	}

	filters := filerepo.ListFilters{
		ObjectType: objectType.String(),
		ObjectID:   entityID,
		Limit:      int(limit),
		Offset:     int(offset),
	}

	if ft := strings.ToLower(c.Query("file_type")); ft != "" {
		filters.FileType = model.GetFileTypeString(ft).String()
	}

	files, _, err := h.fileRepo.List(c, filters)
	if err != nil {
		h.logger.ErrorContext(c, "failed to list files", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to list files")
	}

	items := make([]fileResponse, 0, len(files))
	for i := range files {
		items = append(items, *h.mapFile(files[i]))
	}

	return c.JSON(listResponse{
		Files:    items,
		EntityID: entityID,
	})
}

// GetFile returns upload task status or file details.
func (h *Handler) GetFile(c fiber.Ctx) error {
	fileIDStr := c.Params("id")
	fileID, err := strconv.ParseInt(fileIDStr, 10, 64)
	if err != nil || fileID <= 0 {
		return h.jsonError(c, fiber.StatusBadRequest, "invalid task id")
	}

	fileModel, err := h.taskUploader.GetFile(c, fileID)
	if err != nil {
		if errors.Is(err, uploadservice.ErrTaskNotFound) {
			return h.jsonError(c, fiber.StatusNotFound, "task not found")
		}

		h.logger.ErrorContext(c, "failed to fetch upload task", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to load task")
	}

	response := uploadFileResponse{
		File: h.mapFile(fileModel),
	}

	return c.JSON(response)
}

// DeleteFile deletes a file.
func (h *Handler) DeleteFile(c fiber.Ctx) error {
	fileID, err := h.parseFileID(c)
	if err != nil {
		return h.jsonError(c, fiber.StatusBadRequest, err.Error())
	}

	if !h.isConfirmed(c) {
		return h.jsonError(c, fiber.StatusBadRequest, "confirmation required")
	}

	file, err := h.fileRepo.GetByID(c, fileID)
	if err != nil {
		h.logger.ErrorContext(c, "failed to load file", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to load file")
	}
	if file.ID == 0 {
		return h.jsonError(c, fiber.StatusNotFound, "file not found")
	}

	// Context might be needed for permission check or user tracking
	uCtx, err := h.contextBuilder(c, nil)
	if err != nil {
		return h.jsonError(c, fiber.StatusUnauthorized, err.Error())
	}

	if err := h.taskUploader.DeleteFile(c, uploadservice.DeleteRequest{
		UserRequestID: uCtx.UserUUID,
		FileID:        fileID,
	}); err != nil {
		h.logger.ErrorContext(c, "failed to enqueue delete", logger.Error(err))
		return h.jsonError(c, fiber.StatusInternalServerError, "failed to enqueue delete task")
	}

	return c.Status(http.StatusAccepted).JSON(deleteResponse{
		Status:   deleteStatusPending,
		ID:       file.ID,
		EntityID: file.ObjectID.Int64(),
	})
}

func (h *Handler) getStrategy(contextName string) uploadstrategies.Strategy {
	if h.strategies == nil {
		return nil
	}
	return h.strategies[strings.ToLower(contextName)]
}

// Helpers

func setTusHeaders(c fiber.Ctx) {
	c.Set("Tus-Resumable", tusupload.Version)
	c.Set("Access-Control-Expose-Headers", tusupload.ExposeHeaders)
}

func isTusResumable(c fiber.Ctx) bool {
	return strings.TrimSpace(c.Get("Tus-Resumable")) == tusupload.Version
}

func (h *Handler) isTusOwner(uCtx uploadstrategies.UploadContext, session tusupload.Session) bool {
	return session.OwnerID == 0 || session.OwnerID == uCtx.UserID
}

func (h *Handler) parseFileID(c fiber.Ctx) (int64, error) {
	if idStr := c.Params("id"); idStr != "" {
		if id, err := strconv.ParseInt(idStr, 10, 64); err == nil {
			if id > 0 {
				return id, nil
			}
			return 0, errors.New("invalid file id")
		}
		return 0, errors.New("invalid file id")
	}

	idStr := c.FormValue("fileId")
	if idStr == "" {
		return 0, errors.New("file id is required")
	}
	if id, err := strconv.ParseInt(idStr, 10, 64); err == nil && id > 0 {
		return id, nil
	}
	return 0, errors.New("invalid file id")
}

func (h *Handler) isConfirmed(c fiber.Ctx) bool {
	if strings.EqualFold(c.Query("confirm"), "true") {
		return true
	}
	if strings.EqualFold(c.FormValue("confirm"), "true") {
		return true
	}

	return false
}

func (h *Handler) resolveConfig(
	objectType fileshared.FileObjectType,
	objectID fileshared.FileObjectID,
	_ string,
	fileType model.FileType,
) *uploadservice.FileUploadConfig {
	// Fallback logic if no strategy is found
	config := uploadservice.DefaultFileUploadConfig(objectType.String(), objectID.String())

	//nolint:exhaustive // defaults
	switch fileType {
	case model.FileTypeVideo:
		config.MaxFileSize = 50 * 1024 * 1024 // 50MB
		config.AllowedExtensions = []string{".mp4", ".webm"}
		config.AllowedMimeTypes = map[string][]string{
			".mp4":  {"video/mp4"},
			".webm": {"video/webm"},
		}
	case model.FileTypeImage:
		config.MaxFileSize = 10 * 1024 * 1024 // 10MB
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

type resolveUploadStrategyOptions struct {
	checkCanUpload   bool
	includeAfterJobs bool
}

type resolveUploadStrategyErrorKind string

const (
	resolveUploadStrategyErrorForbidden resolveUploadStrategyErrorKind = "forbidden"
	resolveUploadStrategyErrorInternal  resolveUploadStrategyErrorKind = "internal"
)

type resolveUploadStrategyError struct {
	kind resolveUploadStrategyErrorKind
	err  error
}

func (e resolveUploadStrategyError) Error() string {
	return e.err.Error()
}

func (e resolveUploadStrategyError) Unwrap() error {
	return e.err
}

type resolveUploadStrategyResult struct {
	config    *uploadservice.FileUploadConfig
	afterJobs []fileshared.FileEventAfterJob
}

func (h *Handler) resolveUploadStrategy(
	c fiber.Ctx,
	uCtx uploadstrategies.UploadContext,
	objectType fileshared.FileObjectType,
	objectID fileshared.FileObjectID,
	contextValue string,
	fileType model.FileType,
	skipResize bool,
	opts resolveUploadStrategyOptions,
) (resolveUploadStrategyResult, error) {
	strategy := h.getStrategy(contextValue)

	if strategy != nil && opts.checkCanUpload {
		if err := strategy.CanUpload(c, uCtx); err != nil {
			return resolveUploadStrategyResult{}, resolveUploadStrategyError{
				kind: resolveUploadStrategyErrorForbidden,
				err:  err,
			}
		}
	}

	var config *uploadservice.FileUploadConfig
	var afterJobs []fileshared.FileEventAfterJob

	if strategy != nil {
		config = strategy.GetConfig(c, uCtx)
		if opts.includeAfterJobs {
			jobs, err := strategy.GetAfterJobs(c, uCtx)
			if err != nil {
				return resolveUploadStrategyResult{}, resolveUploadStrategyError{
					kind: resolveUploadStrategyErrorInternal,
					err:  err,
				}
			}
			afterJobs = jobs
		}
	} else {
		config = h.resolveConfig(objectType, objectID, contextValue, fileType)
	}
	if config == nil {
		return resolveUploadStrategyResult{}, resolveUploadStrategyError{
			kind: resolveUploadStrategyErrorInternal,
			err:  fmt.Errorf("upload strategy %q returned nil config", contextValue),
		}
	}

	if skipResize {
		config.SkipResizer = true
	}

	return resolveUploadStrategyResult{
		config:    config,
		afterJobs: afterJobs,
	}, nil
}

// --- Request Parsing ---

func (h *Handler) getObjectRequest(c fiber.Ctx) (fileshared.FileObjectType, fileshared.FileObjectID, error) {
	objectTypeValue := c.FormValue("objectType")
	if value := c.FormValue("entity_type"); value != "" {
		objectTypeValue = value
	}

	objectType := fileshared.FileObjectType(objectTypeValue)
	if err := objectType.Validate(); err != nil {
		return objectType, 0, err
	}

	objectIDStr := c.FormValue("objectId")
	if value := c.FormValue("entity_id"); value != "" {
		objectIDStr = value
	}

	var objectID fileshared.FileObjectID
	if objectIDStr != "" {
		if id, err := strconv.ParseInt(objectIDStr, 10, 64); err == nil {
			objectID = fileshared.FileObjectID(id)
		}
	}

	return objectType, objectID, nil
}

func (h *Handler) getDeletedIDRequest(c fiber.Ctx) int64 {
	deleteIDStr := c.FormValue("deleteId")
	if v := c.FormValue("replace_file_id"); v != "" {
		deleteIDStr = v
	}

	var objectID int64
	if deleteIDStr != "" {
		if id, err := strconv.ParseInt(deleteIDStr, 10, 64); err == nil {
			objectID = id
		}
	}

	return objectID
}

func parseBoolFlag(value string) bool {
	if value == "" {
		return false
	}

	normalized := strings.TrimSpace(strings.ToLower(value))
	switch normalized {
	case "true", "1", "yes", "y", "on":
		return true
	case "false", "0", "no", "n", "off":
		return false
	default:
		flag, err := strconv.ParseBool(normalized)
		return err == nil && flag
	}
}

func parseTusLength(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, errors.New("missing Upload-Length")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("invalid Upload-Length")
	}
	return parsed, nil
}

func parseTusOffset(value string) (int64, error) {
	if strings.TrimSpace(value) == "" {
		return 0, errors.New("missing Upload-Offset")
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil || parsed < 0 {
		return 0, errors.New("invalid Upload-Offset")
	}
	return parsed, nil
}

func parseTusMetadata(raw string) (map[string]string, error) {
	result := make(map[string]string)
	if strings.TrimSpace(raw) == "" {
		return result, nil
	}

	pairs := strings.Split(raw, ",")
	for _, pair := range pairs {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		parts := strings.SplitN(pair, " ", 2)
		if len(parts) == 0 || parts[0] == "" {
			continue
		}
		key := strings.TrimSpace(parts[0])
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
		if value, ok := metadata[key]; ok && strings.TrimSpace(value) != "" {
			return value
		}
	}
	return ""
}

func (h *Handler) parseTusObject(metadata map[string]string) (fileshared.FileObjectType, fileshared.FileObjectID, error) {
	rawType := metadataValue(metadata, "entity_type", "object_type", "objectType")
	objectType := fileshared.FileObjectType(rawType)
	if err := objectType.Validate(); err != nil {
		return objectType, 0, errors.New("entity_type is required")
	}

	rawID := metadataValue(metadata, "entity_id", "object_id", "objectId")
	if rawID == "" {
		return objectType, 0, errors.New("entity_id is required")
	}

	parsedID, err := strconv.ParseInt(rawID, 10, 64)
	if err != nil || parsedID <= 0 {
		return objectType, 0, errors.New("invalid entity_id")
	}

	return objectType, fileshared.FileObjectID(parsedID), nil
}

func (h *Handler) resolveTusPatchMimeType(
	c fiber.Ctx,
	session tusupload.Session,
	body []byte,
	offset int64,
	cmsOnly bool,
) (string, error) {
	if offset != 0 {
		return "", nil
	}

	metadata := session.Metadata
	filename := strings.TrimSpace(metadataValue(metadata, "filename", "file_name", "fileName"))
	ext := strings.ToLower(filepath.Ext(filename))

	var objectType fileshared.FileObjectType
	var objectID fileshared.FileObjectID
	var err error
	contextValue := strings.ToLower(strings.TrimSpace(metadataValue(metadata, "context")))
	if cmsOnly {
		if contextValue != "cms" || h.getStrategy(contextValue) == nil {
			return "", h.jsonError(c, fiber.StatusInternalServerError, "CMS upload strategy is not configured")
		}
	} else {
		objectType, objectID, err = h.parseTusObject(metadata)
		if err != nil {
			return "", h.jsonError(c, fiber.StatusBadRequest, err.Error())
		}
	}

	fileTypeValue := strings.ToLower(strings.TrimSpace(metadataValue(metadata, "file_type")))
	fileType := model.GetFileTypeString(fileTypeValue)
	skipResize := parseBoolFlag(metadataValue(metadata, "skip_resize"))

	uCtx, _ := h.contextBuilder(c, metadata)
	resolution, err := h.resolveUploadStrategy(
		c,
		uCtx,
		objectType,
		objectID,
		contextValue,
		fileType,
		skipResize,
		resolveUploadStrategyOptions{},
	)
	if err != nil {
		h.logger.ErrorContext(c, "failed to resolve upload strategy", logger.Error(err))
		return "", h.jsonError(c, fiber.StatusInternalServerError, "failed to process upload")
	}

	config := resolution.config
	if config == nil {
		return "", h.jsonError(c, fiber.StatusInternalServerError, "failed to process upload")
	}

	sniff := body
	if len(sniff) > tusupload.SniffLen {
		sniff = sniff[:tusupload.SniffLen]
	}
	mimeType := http.DetectContentType(sniff)
	if !isAllowedMimeType(ext, mimeType, config.AllowedMimeTypes) {
		return "", h.jsonError(c, fiber.StatusBadRequest, "file mime type is not allowed")
	}

	return mimeType, nil
}

func parseInt64(value string) int64 {
	if strings.TrimSpace(value) == "" {
		return 0
	}
	parsed, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return 0
	}
	return parsed
}

func isAllowedExtension(ext string, allowed []string) bool {
	ext = strings.ToLower(strings.TrimSpace(ext))
	if ext == "" || len(allowed) == 0 {
		return true
	}

	for _, item := range allowed {
		if strings.ToLower(strings.TrimSpace(item)) == ext {
			return true
		}
	}

	return false
}

func isAllowedMimeType(ext string, mime string, allowed map[string][]string) bool {
	ext = strings.ToLower(strings.TrimSpace(ext))
	mime = strings.ToLower(strings.TrimSpace(mime))
	if idx := strings.Index(mime, ";"); idx >= 0 {
		mime = strings.TrimSpace(mime[:idx])
	}
	if len(allowed) == 0 {
		return true
	}
	allowedMimes, ok := allowed[ext]
	if !ok || len(allowedMimes) == 0 {
		return false
	}
	for _, item := range allowedMimes {
		if strings.ToLower(strings.TrimSpace(item)) == mime {
			return true
		}
	}
	return false
}

// --- Response Mapping ---

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

func (h *Handler) mapFile(model model.File) *fileResponse {
	status := model.GetData().Uploader.Status.String()
	if status == "" {
		status = fileshared.FileUploadTaskStatusCompleted.String()
	}

	url := ""
	publicURL := ""
	thumbnailURL := ""
	fullPath := ""
	folderPath := ""
	if status == fileshared.FileUploadTaskStatusCompleted.String() {
		publicURL = h.urlComposer(model.GetPublicURL())
		url = publicURL
		if model.URL != "" {
			url = h.urlComposer(model.URL)
		}
		thumbnailURL = model.PreferredPreviewPath()
		if thumbnailURL != "" {
			thumbnailURL = h.urlComposer(thumbnailURL)
		}
		fullPath = model.GetFullPath()
		folderPath = model.FolderPath
	}

	return &fileResponse{
		ID:           model.ID,
		EntityID:     model.ObjectID.Int64(),
		Filename:     model.FileName,
		OriginalName: model.OriginalFileName,
		FileType:     model.FileType.String(),
		MimeType:     model.MimeType,
		Size:         model.Size,
		URL:          url,
		PublicURL:    publicURL,
		ThumbnailURL: thumbnailURL,
		FullPath:     fullPath,
		FolderPath:   folderPath,
		SortOrder:    model.Position,
		Status:       status,
		IsPrimary:    model.IsPrimary,
		Width:        model.GetWidth(),
		Height:       model.GetHeight(),
		CreatedAt:    pointerTime(model.CreatedAt),
		UpdatedAt:    pointerTime(model.UpdatedAt),
		Data:         buildFileDataResponse(model),
	}
}

func pointerTime(t time.Time) *time.Time {
	if t.IsZero() {
		return nil
	}
	tt := t
	return &tt
}

func buildFileDataResponse(file model.File) *fileDataResponse {
	data := file.GetData()
	if data == nil {
		return nil
	}

	resp := &fileDataResponse{
		Width:  data.Width,
		Height: data.Height,
	}

	if data.Alt != "" {
		resp.Alt = data.Alt
	}

	if driver := data.Provider.Driver; driver != "" {
		resp.Provider = &fileProviderResponse{Driver: driver}
	}

	return resp
}

func (h *Handler) jsonError(c fiber.Ctx, status int, message string) error {
	return c.Status(status).JSON(errorResponse{
		Status: "error",
		Error:  message,
	})
}

func (h *Handler) jsonErrorWithDetail(c fiber.Ctx, status int, message string, err error) error {
	if err != nil {
		h.logger.DebugContext(c, "request validation failed", logger.Error(err))
	}
	return h.jsonError(c, status, message)
}

func (h *Handler) jsonValidationError(c fiber.Ctx, errors map[string]string) error {
	return c.Status(fiber.StatusBadRequest).JSON(validationResponse{
		Status: "error",
		Errors: errors,
	})
}

// --- DTOs ---

type uploadListResponse struct {
	Files []fileResponse `json:"files,omitempty"`
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

const (
	deleteStatusPending = "pending"
)
