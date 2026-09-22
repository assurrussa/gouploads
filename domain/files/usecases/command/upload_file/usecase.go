package uploadfile

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/goshared/pkg/filesanitize"
	commonshared "github.com/assurrussa/gouploads/domain/files/model"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	eventstream "github.com/assurrussa/gowebsocket/eventstream"
	sharedjob "github.com/assurrussa/outbox/shared/job"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/google/uuid"

	"github.com/assurrussa/gouploads/domain/files/model"
	deletedfilejob "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	eventfileafterprocess "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process"
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

const (
	contentTypeImageWebP = "image/webp"
	extensionWebP        = ".webp"
	maxArtifactSize      = int64(10 << 30)
	artifactSniffSize    = 512
	failedCleanupTimeout = 30 * time.Second
)

//go:generate toolsmocks

type fileRepository interface {
	GetByID(ctx context.Context, fileID int64) (model.File, error)
	Update(ctx context.Context, fileID int64, file model.File) error
}

type outboxPutter interface {
	Put(ctx context.Context, name, payload string, availableAt time.Time) (outboxtypes.JobID, error)
}

type fileStorage interface {
	SavePersist(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error)
	Delete(ctx context.Context, relativePath string) error
}

type resizeClient interface {
	DownloadFile(ctx context.Context, req clientresizer.RequestDownload) (clientresizer.ResponseDownload, error)
}

type transactor interface {
	RunInTx(ctx context.Context, f func(context.Context) error) error
}

type fileStorageDTO struct {
	RelativePath string
	FolderPath   string
	FileName     string
	URL          string
	Size         int64
	MimeType     string
	Width        int
	Height       int
	Checksum     string
}

//go:generate options-gen -out-filename=usecase_options.gen.go -from-struct=Options
type Options struct {
	transactor      transactor              `option:"mandatory" validate:"required"`
	fileRepository  fileRepository          `option:"mandatory" validate:"required"`
	resizeClient    resizeClient            `option:"mandatory" validate:"required"`
	eventStream     eventstream.EventStream `option:"mandatory" validate:"required"`
	logger          logger.Logger           `option:"mandatory" validate:"required"`
	storage         fileStorage             `option:"mandatory" validate:"required"`
	outbox          outboxPutter            `option:"mandatory" validate:"required"`
	baseFolder      string                  `default:"media/v1"`
	deliveryBaseURL string                  `validate:"omitempty,url"`
}

type UseCase struct {
	sharedjob.DefaultJob
	Options
}

func Must(opts Options) *UseCase {
	j, err := New(opts)
	if err != nil {
		panic(err)
	}

	return j
}

func New(opts Options) (*UseCase, error) {
	if err := opts.Validate(); err != nil {
		return nil, fmt.Errorf("validate job options: %w", err)
	}

	return &UseCase{
		Options: opts,
	}, nil
}

func (u *UseCase) Handle(ctx context.Context, req Request) (Response, error) {
	if err := req.Validate(); err != nil {
		return Response{}, fmt.Errorf("validate request: %w", err)
	}

	fileModel, err := u.fileRepository.GetByID(ctx, req.FileID)
	if err != nil {
		return Response{}, fmt.Errorf("get task %d: %w", req.FileID, err)
	}

	fileUploader := fileModel.GetData().Uploader
	taskLogger := u.logger.WithAttrs(
		slog.Int64("file_id", fileModel.ID),
		slog.String("status", fileUploader.Status.String()),
	)

	if isCompletedFile(fileModel, fileUploader) {
		taskLogger.DebugContext(ctx, "task already completed")
		return Response{}, nil
	}

	sourcePath := fileModel.GetFullPath()

	fileModel, finalPaths, err := u.processFileModel(ctx, req, fileModel)
	if err != nil {
		if req.CleanupOnFailure {
			u.scheduleFailedFinalCleanup(ctx, fileUploader.UserUUID, finalPaths)
		}
		return Response{}, fmt.Errorf("process file model: %w", err)
	}

	err = u.transactor.RunInTx(ctx, func(ctx context.Context) error {
		if err := u.fileRepository.Update(ctx, fileModel.ID, fileModel); err != nil {
			return fmt.Errorf("save file: %w", err)
		}
		if err := u.enqueueAfterJobs(ctx, fileUploader, fileModel); err != nil {
			return fmt.Errorf("schedule after jobs: %w", err)
		}
		if sourcePath != "" {
			if err := u.enqueueCleanup(ctx, fileUploader.UserUUID, sourcePath); err != nil {
				return fmt.Errorf("schedule staging cleanup: %w", err)
			}
		}

		return nil
	})
	if err != nil {
		if req.CleanupOnFailure {
			u.scheduleFailedFinalCleanup(ctx, fileUploader.UserUUID, finalPaths)
		}
		return Response{}, fmt.Errorf("RunInTx: %w", err)
	}
	taskLogger.InfoContext(
		ctx,
		"scheduled media cleanup",
		slog.Int64("deleted_id", replacementDeletedID(fileUploader.AfterJobs)),
		slog.String("staging_key", sourcePath),
	)

	eventModel := fileModel
	eventModel.URL = u.publicURL(fileModel.GetFullPath())
	u.publish(ctx, fileUploader.UserUUID, createEvent(fileUploader, eventModel, shared.FileUploadTaskStatusCompleted))

	return Response{}, nil
}

func replacementDeletedID(afterJobs []shared.FileEventAfterJob) int64 {
	for _, afterJob := range afterJobs {
		if strings.TrimSpace(afterJob.JobName) != deletedfilejob.JobName || strings.TrimSpace(afterJob.Payload) == "" {
			continue
		}
		payload, err := deletedfilejob.UnmarshalPayload(afterJob.Payload)
		if err == nil && payload.FileID > 0 {
			return payload.FileID
		}
	}

	return 0
}

func (u *UseCase) enqueueAfterJobs(
	ctx context.Context,
	uploader shared.FileUploader,
	fileModel model.File,
) error {
	for _, afterJob := range uploader.AfterJobs {
		jobName := strings.TrimSpace(afterJob.JobName)
		if jobName == "" {
			continue
		}

		payload, err := u.createPayload(fileModel, afterJob, uploader)
		if err != nil {
			return fmt.Errorf("outbox create payload: %w", err)
		}

		if _, err := u.outbox.Put(ctx, jobName, payload, time.Now()); err != nil {
			return fmt.Errorf("outbox put: %w", err)
		}
	}

	return nil
}

func (u *UseCase) enqueueCleanup(ctx context.Context, userID sharedtypes.UserID, objectPath string) error {
	payload, err := deletedfilejob.MarshalPayload(deletedfilejob.NewPayload(0, userID, objectPath))
	if err != nil {
		return fmt.Errorf("marshal cleanup payload: %w", err)
	}
	if _, err := u.outbox.Put(ctx, deletedfilejob.JobName, payload, time.Now()); err != nil {
		return fmt.Errorf("put cleanup job: %w", err)
	}

	return nil
}

func (u *UseCase) scheduleFailedFinalCleanup(
	ctx context.Context,
	userID sharedtypes.UserID,
	paths []string,
) {
	cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failedCleanupTimeout)
	defer cancel()

	for _, objectPath := range paths {
		if err := u.enqueueCleanup(cleanupCtx, userID, objectPath); err == nil {
			continue
		}
		if err := u.removeFile(cleanupCtx, objectPath, "cleanup partial final object"); err != nil {
			u.logger.ErrorContext(cleanupCtx, "failed to clean partial final object", slog.String("path", objectPath), logger.Error(err))
		}
	}
}

func isCompletedFile(fileModel model.File, uploader shared.FileUploader) bool {
	if uploader.Status == shared.FileUploadTaskStatusCompleted {
		return true
	}

	return uploader.Status == "" && len(fileModel.GetData().Presets) > 0
}

func (u *UseCase) createPayload(
	fileModel model.File,
	afterJob shared.FileEventAfterJob,
	uploader shared.FileUploader,
) (string, error) {
	if afterJob.Payload != "" {
		return afterJob.Payload, nil
	}

	meta := map[string]any{
		"fileId":           fileModel.ID,
		"fileUrl":          u.publicURL(fileModel.GetFullPath()),
		"fileName":         fileModel.FileName,
		"originalFileName": fileModel.OriginalFileName,
		"objectType":       fileModel.ObjectType.String(),
		"objectId":         fileModel.ObjectID.String(),
	}

	if len(afterJob.Meta) > 0 {
		for k, v := range afterJob.Meta {
			meta[k] = v
		}
	}

	pl := eventfileafterprocess.NewPayload(uploader.UserUUID, uploader.Type, fileModel.ID, afterJob.JobName, meta)
	payload, err := eventfileafterprocess.MarshalPayload(pl)
	if err != nil {
		return "", fmt.Errorf("marshal after job payload: %w", err)
	}

	return payload, nil
}

func (u *UseCase) publish(ctx context.Context, userID sharedtypes.UserID, event shared.FileUploadStatusEvent) {
	if err := u.eventStream.Publish(ctx, userID, event); err != nil {
		u.logger.WarnContext(ctx, "publish event", logger.Error(err))
	}
}

func (u *UseCase) processFileModel(
	ctx context.Context,
	req Request,
	fileModel model.File,
) (model.File, []string, error) {
	fileData := fileModel.GetData()
	presets := make(map[shared.PresetName]shared.FilePreset, len(req.Artifacts))
	finalPaths := make([]string, 0, len(req.Artifacts))
	hasMainArtifact := false

	for _, artifact := range req.Artifacts {
		if artifact.ExpireAt.Before(time.Now()) {
			return fileModel, finalPaths, fmt.Errorf("artifact %q is expired", artifact.Preset)
		}
		isMain := isMainArtifact(artifact)

		storageFile, err := u.saveFileStorage(ctx, artifact, fileModel)
		if storageFile.RelativePath != "" {
			finalPaths = append(finalPaths, storageFile.RelativePath)
		}
		if err != nil {
			return fileModel, finalPaths, fmt.Errorf("prepare storage: %w", err)
		}

		if isMain {
			hasMainArtifact = true
			if err := u.updateMainArtifact(&fileModel, fileData, storageFile, artifact.Metadata); err != nil {
				return fileModel, finalPaths, err
			}
		}

		lowerPreset := strings.ToLower(artifact.Preset)
		presets[shared.PresetName(artifact.Preset)] = shared.FilePreset{
			PresetName:     artifact.Preset,
			Size:           storageFile.Size,
			MimeType:       storageFile.MimeType,
			URL:            "",
			Width:          storageFile.Width,
			Height:         storageFile.Height,
			RelativePath:   storageFile.RelativePath,
			ChecksumSHA256: storageFile.Checksum,
			MediaType:      artifact.MediaType,
			IsPreview:      metadataBool(artifact.Metadata, "preview") || strings.Contains(lowerPreset, "preview"),
			IsThumbnail:    metadataBool(artifact.Metadata, "thumbnail") || strings.Contains(lowerPreset, "thumbnail"),
		}
	}
	if !hasMainArtifact {
		return fileModel, finalPaths, errors.New("finalization has no main artifact")
	}

	fileData.Presets = presets
	fileData.Uploader = shared.FileUploader{}
	fileModel.SetData(fileData)

	return fileModel, finalPaths, nil
}

func (u *UseCase) updateMainArtifact(
	fileModel *model.File,
	fileData *model.FileData,
	storageFile fileStorageDTO,
	metadata map[string]any,
) error {
	// Persist primary artifact metadata on the file model.
	fileModel.FolderPath = storageFile.FolderPath
	fileModel.FileName = storageFile.FileName
	fileModel.Size = storageFile.Size
	fileModel.MimeType = storageFile.MimeType
	fileModel.URL = ""

	width := storageFile.Width
	if width <= 0 {
		width = metadataInt(metadata, "target_width")
	}
	if width > 0 {
		fileData.Width = width
	}

	height := storageFile.Height
	if height <= 0 {
		height = metadataInt(metadata, "target_height")
	}
	if height > 0 {
		fileData.Height = height
	}

	fileType, err := commonshared.GetFileTypeFromMimeType(storageFile.MimeType)
	if err != nil {
		return fmt.Errorf("prepare mime type for file type: %w", err)
	}
	fileModel.FileType = fileType

	return nil
}

func (u *UseCase) saveFileStorage(
	ctx context.Context,
	artifact Artifact,
	fileModel model.File,
) (fileStorageDTO, error) {
	respDownload, err := u.resizeClient.DownloadFile(ctx, clientresizer.RequestDownload{
		Preset:    artifact.Preset,
		URL:       artifact.URL,
		TypeMedia: artifact.MediaType,
	})
	if err != nil {
		return fileStorageDTO{}, fmt.Errorf("download file resize preset: %w", err)
	}
	defer func() { _ = respDownload.Body.Close() }()

	if artifact.Size > maxArtifactSize || respDownload.ContentLength > maxArtifactSize {
		return fileStorageDTO{}, fmt.Errorf("artifact exceeds maximum size %d", maxArtifactSize)
	}
	if artifact.Size > 0 && respDownload.ContentLength > 0 && artifact.Size != respDownload.ContentLength {
		return fileStorageDTO{}, fmt.Errorf(
			"artifact declared size mismatch: webhook=%d response=%d",
			artifact.Size, respDownload.ContentLength,
		)
	}

	reader := bufio.NewReaderSize(respDownload.Body, artifactSniffSize)
	header, peekErr := reader.Peek(artifactSniffSize)
	if peekErr != nil && !errors.Is(peekErr, io.EOF) && !errors.Is(peekErr, bufio.ErrBufferFull) {
		return fileStorageDTO{}, fmt.Errorf("inspect artifact body: %w", peekErr)
	}
	if len(header) == 0 {
		return fileStorageDTO{}, errors.New("artifact body is empty")
	}

	fileMimeType, err := validateArtifactContentType(
		artifact.ContentType,
		respDownload.ContentType,
		http.DetectContentType(header),
	)
	if err != nil {
		return fileStorageDTO{}, err
	}
	finalDir, err := u.finalArtifactDir(fileModel)
	if err != nil {
		return fileStorageDTO{}, err
	}
	fileName, err := artifactPresetFileName(artifact, fileMimeType)
	if err != nil {
		return fileStorageDTO{}, err
	}

	hasher := sha256.New()
	counter := &artifactReader{
		Reader: io.TeeReader(io.LimitReader(reader, maxArtifactSize+1), hasher),
	}

	stored, err := u.storage.SavePersist(ctx, filestorage.SaveFileInput{
		Dir:      finalDir,
		FileName: fileName,
		Size:     artifact.Size,
		MimeType: fileMimeType,
		Reader:   counter,
	})
	if err != nil {
		return fileStorageDTO{}, fmt.Errorf("commit file: %w", err)
	}

	result := fileStorageDTO{
		RelativePath: stored.RelativePath,
		FolderPath:   filesanitize.EnsureRelativeDir(stored.RelativePath),
		FileName:     filepath.Base(stored.RelativePath),
		URL:          "",
		Size:         counter.Size(),
		MimeType:     stored.MimeType,
		Width:        metadataInt(artifact.Metadata, "target_width"),
		Height:       metadataInt(artifact.Metadata, "target_height"),
		Checksum:     hex.EncodeToString(hasher.Sum(nil)),
	}
	if result.Size > maxArtifactSize {
		return result, fmt.Errorf("artifact exceeds maximum size %d", maxArtifactSize)
	}
	if artifact.Size > 0 && result.Size != artifact.Size {
		return result, fmt.Errorf("artifact size mismatch: expected=%d actual=%d", artifact.Size, result.Size)
	}
	if respDownload.ContentLength > 0 && result.Size != respDownload.ContentLength {
		return result, fmt.Errorf(
			"artifact response size mismatch: expected=%d actual=%d",
			respDownload.ContentLength, result.Size,
		)
	}
	if stored.Size > 0 && stored.Size != result.Size {
		return result, fmt.Errorf("stored artifact size mismatch: expected=%d actual=%d", result.Size, stored.Size)
	}

	return result, nil
}

type artifactReader struct {
	io.Reader
	n int64
}

func (r *artifactReader) Read(p []byte) (int, error) {
	n, err := r.Reader.Read(p)
	r.n += int64(n)
	return n, err
}

func (r *artifactReader) Size() int64 {
	return r.n
}

func isMainArtifact(artifact Artifact) bool {
	// Artifacts explicitly marked as preview/thumbnail should never become the main file.
	if metadataBool(artifact.Metadata, "preview") || metadataBool(artifact.Metadata, "thumbnail") {
		return false
	}

	if artifact.Preset == shared.FilePresetMainName.String() {
		return true
	}

	return isVideoArtifact(artifact)
}

func isVideoArtifact(artifact Artifact) bool {
	contentType := strings.ToLower(artifact.ContentType)
	if strings.HasPrefix(contentType, "image/") {
		return false
	}

	if strings.HasPrefix(contentType, "video/") {
		return true
	}

	if strings.EqualFold(strings.TrimSpace(artifact.MediaType), "video") {
		return true
	}

	if kind := strings.ToLower(metadataString(artifact.Metadata, "media_type")); kind == "video" {
		return true
	}

	return false
}

func metadataBool(meta map[string]any, key string) bool {
	if meta == nil {
		return false
	}

	value, ok := meta[key]
	if !ok {
		return false
	}

	switch v := value.(type) {
	case bool:
		return v
	case string:
		b, err := strconv.ParseBool(v)
		if err == nil {
			return b
		}
	case float64:
		return v != 0
	case int:
		return v != 0
	case int64:
		return v != 0
	case json.Number:
		b, err := strconv.ParseBool(v.String())
		if err == nil {
			return b
		}
	}

	return false
}

func metadataInt(meta map[string]any, key string) int {
	if meta == nil {
		return 0
	}

	value, ok := meta[key]
	if !ok {
		return 0
	}

	switch v := value.(type) {
	case int:
		return v
	case int64:
		return int(v)
	case float64:
		return int(v)
	case string:
		n, err := strconv.Atoi(v)
		if err == nil {
			return n
		}
	case json.Number:
		n, err := v.Int64()
		if err == nil {
			return int(n)
		}
	}

	return 0
}

func metadataString(meta map[string]any, key string) string {
	if meta == nil {
		return ""
	}

	value, ok := meta[key]
	if !ok {
		return ""
	}

	switch v := value.(type) {
	case string:
		return v
	case fmt.Stringer:
		return v.String()
	}

	return ""
}

func (u *UseCase) removeFile(ctx context.Context, relPath, reason string) error {
	if err := u.storage.Delete(ctx, relPath); err != nil && !errors.Is(err, filestorage.ErrNotSupported) {
		u.logger.WarnContext(ctx, reason, slog.String("path", relPath), logger.Error(err))

		return err
	}

	return nil
}

func (u *UseCase) finalArtifactDir(fileModel model.File) (string, error) {
	if err := fileModel.ObjectType.Validate(); err != nil {
		return "", fmt.Errorf("validate final object type: %w", err)
	}
	if fileModel.ObjectID == nil || fileModel.ObjectID.Int64() <= 0 {
		return "", errors.New("final object id is required")
	}
	if _, err := uuid.Parse(fileModel.Slug); err != nil {
		return "", fmt.Errorf("validate final file slug: %w", err)
	}

	segments := strings.Split(strings.Trim(u.baseFolder, "/"), "/")
	segments = append(
		segments,
		fileModel.ObjectType.String(),
		fileModel.ObjectID.String(),
		fileModel.Slug,
	)
	finalDir, err := filesanitize.BuildSafePath(segments...)
	if err != nil {
		return "", fmt.Errorf("build final artifact directory: %w", err)
	}

	return finalDir, nil
}

func (u *UseCase) publicURL(relativePath string) string {
	return fileurl.Compose(u.deliveryBaseURL, "", relativePath)
}

func artifactPresetFileName(artifact Artifact, contentType string) (string, error) {
	preset, err := filesanitize.SanitizeSegment(artifact.Preset)
	if err != nil {
		return "", fmt.Errorf("sanitize artifact preset: %w", err)
	}
	extension := extensionFromContentType(contentType)
	if extension == "" {
		return "", fmt.Errorf("artifact content type %s has no supported extension", contentType)
	}
	name, err := filesanitize.SanitizeFileName(preset + extension)
	if err != nil {
		return "", fmt.Errorf("sanitize artifact file name: %w", err)
	}

	return name, nil
}

func validateArtifactContentType(webhookType, responseType, detectedType string) (string, error) {
	types := []string{
		normalizeContentType(webhookType),
		normalizeContentType(responseType),
		normalizeContentType(detectedType),
	}
	selected := ""
	for _, contentType := range types {
		if contentType == "" || contentType == "application/octet-stream" {
			continue
		}
		if selected == "" {
			selected = contentType
			continue
		}
		if selected != contentType {
			return "", fmt.Errorf("artifact content type mismatch: %s != %s", selected, contentType)
		}
	}
	if selected == "" {
		return "", errors.New("artifact content type is unknown")
	}
	if !allowedArtifactContentType(selected) {
		return "", fmt.Errorf("artifact content type %s is not allowed", selected)
	}

	return selected, nil
}

func allowedArtifactContentType(contentType string) bool {
	switch contentType {
	case "image/jpeg", "image/png", "image/gif", "image/webp",
		"video/mp4", "video/webm", "application/pdf":
		return true
	default:
		return false
	}
}

func normalizeContentType(raw string) string {
	contentType, _, err := mime.ParseMediaType(strings.TrimSpace(raw))
	if err != nil {
		return ""
	}

	return strings.ToLower(contentType)
}

func extensionFromContentType(contentType string) string {
	if contentType == "" {
		return ""
	}

	if exts, _ := mime.ExtensionsByType(contentType); len(exts) > 0 {
		return strings.ToLower(exts[0])
	}

	// Common fallback when mime package is missing mapping.
	contentTypeLower := strings.ToLower(contentType)
	if contentTypeLower == contentTypeImageWebP {
		return extensionWebP
	}

	return ""
}

func createEvent(
	uploader shared.FileUploader,
	fileModel model.File,
	status shared.FileUploadTaskStatus,
) shared.FileUploadStatusEvent {
	eventFileSendResizer := shared.NewFileUploadStatusEvent(fileModel.ID, status)
	eventFileSendResizer.Metadata = map[string]any{
		"name":         "upload_file",
		"uploaderUuid": uploader.UserUUID.String(),
		"objectType":   fileModel.ObjectType.String(),
		"objectId":     fileModel.ObjectID.String(),
	}
	eventFileSendResizer.File = buildEventFileEnvelope(fileModel)

	return eventFileSendResizer
}

func buildEventFileEnvelope(fileModel model.File) *shared.FileUploadEventFile {
	return &shared.FileUploadEventFile{
		ID:           fileModel.ID,
		FileName:     fileModel.FileName,
		OriginalName: fileModel.OriginalFileName,
		URL:          fileModel.GetPublicURL(),
		Size:         fileModel.Size,
		MimeType:     fileModel.MimeType,
		Width:        fileModel.GetWidth(),
		Height:       fileModel.GetHeight(),
		IsPrimary:    fileModel.IsPrimary,
	}
}
