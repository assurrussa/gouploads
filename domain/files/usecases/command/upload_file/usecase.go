package uploadfile

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	"io"
	"log/slog"
	"mime"
	"net/http"
	"net/url"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/assurrussa/goshared/pkg/filesanitize"
	commonshared "github.com/assurrussa/goshared/pkg/filetypes"
	logger "github.com/assurrussa/goshared/pkg/logger"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	eventstream "github.com/assurrussa/goshared/services/event-stream"
	sharedjob "github.com/assurrussa/outbox/shared/job"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/google/uuid"

	"github.com/assurrussa/gouploads/domain/files/model"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	eventfileafterprocess "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process"
	"github.com/assurrussa/gouploads/domain/files/shared"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

const (
	contentTypeImageWebP = "image/webp"
	extensionWebP        = ".webp"
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
}

//go:generate options-gen -out-filename=usecase_options.gen.go -from-struct=Options
type Options struct {
	transactor     transactor              `option:"mandatory" validate:"required"`
	fileRepository fileRepository          `option:"mandatory" validate:"required"`
	resizeClient   resizeClient            `option:"mandatory" validate:"required"`
	eventStream    eventstream.EventStream `option:"mandatory" validate:"required"`
	logger         logger.Logger           `option:"mandatory" validate:"required"`
	storage        fileStorage             `option:"mandatory" validate:"required"`
	outbox         outboxPutter            `option:"mandatory" validate:"required"`
	baseFolder     string                  `default:"uploads"`
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

	if fileUploader.Status == shared.FileUploadTaskStatusCompleted {
		taskLogger.DebugContext(ctx, "task already completed")
		return Response{}, nil
	}

	sourcePath := fileModel.GetFullPath()

	fileModel, err = u.processFileModel(ctx, req, fileModel)
	if err != nil {
		return Response{}, fmt.Errorf("process file model: %w", err)
	}

	if sourcePath != "" {
		if err := u.removeFile(ctx, sourcePath, "cleanup temp file"); err != nil {
			return Response{}, fmt.Errorf("remove temp file: %w", err)
		}
	}

	err = u.transactor.RunInTx(ctx, func(ctx context.Context) error {
		if err := u.enqueueAfterJobs(ctx, fileUploader, fileModel); err != nil {
			return fmt.Errorf("schedule after jobs: %w", err)
		}

		if err := u.fileRepository.Update(ctx, fileModel.ID, fileModel); err != nil {
			return fmt.Errorf("save file: %w", err)
		}

		return nil
	})
	if err != nil {
		return Response{}, fmt.Errorf("RunInTx: %w", err)
	}

	u.publish(ctx, fileUploader.UserUUID, createEvent(fileUploader, fileModel, shared.FileUploadTaskStatusCompleted))

	return Response{}, nil
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
		"fileUrl":          fileModel.URL,
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

func (u *UseCase) processFileModel(ctx context.Context, req Request, fileModel model.File) (model.File, error) {
	fileData := fileModel.GetData()
	presets := make(map[shared.PresetName]shared.FilePreset, len(req.Artifacts))

	for _, artifact := range req.Artifacts {
		if artifact.ExpireAt.Before(time.Now()) {
			// нет смысла запрашивать просроченные файлы по времени.
			continue
		}
		isMain := isMainArtifact(artifact)

		storageFile, err := u.saveFileStorage(ctx, artifact, fileModel, isMain)
		if err != nil {
			return fileModel, fmt.Errorf("prepare storage: %w", err)
		}

		if isMain {
			if err := u.updateMainArtifact(&fileModel, fileData, storageFile, artifact.Metadata); err != nil {
				return fileModel, err
			}
		}

		lowerPreset := strings.ToLower(artifact.Preset)
		presets[shared.PresetName(artifact.Preset)] = shared.FilePreset{
			PresetName:   artifact.Preset,
			Size:         storageFile.Size,
			MimeType:     storageFile.MimeType,
			URL:          storageFile.URL,
			Width:        storageFile.Width,
			Height:       storageFile.Height,
			RelativePath: storageFile.RelativePath,
			MediaType:    artifact.MediaType,
			IsPreview:    metadataBool(artifact.Metadata, "preview") || strings.Contains(lowerPreset, "preview"),
			IsThumbnail:  metadataBool(artifact.Metadata, "thumbnail") || strings.Contains(lowerPreset, "thumbnail"),
		}
	}

	fileData.Presets = presets
	fileData.Uploader = shared.FileUploader{}
	fileModel.SetData(fileData)

	return fileModel, nil
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
	fileModel.URL = storageFile.URL

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
	isMain bool,
) (fileStorageDTO, error) {
	respDownload, err := u.resizeClient.DownloadFile(ctx, clientresizer.RequestDownload{
		Preset:    artifact.Preset,
		URL:       artifact.URL,
		TypeMedia: artifact.MediaType,
	})
	if err != nil {
		return fileStorageDTO{}, fmt.Errorf("download file resize preset: %w", err)
	}

	folderPathWithPreset := fileModel.FolderPath
	if !isMain {
		folderPathWithPreset = path.Join(folderPathWithPreset, artifact.Preset)
	}
	fileMimeType := http.DetectContentType(respDownload.Body)
	if fileMimeType == "" || fileMimeType == "application/octet-stream" {
		if ct := strings.TrimSpace(artifact.ContentType); ct != "" {
			fileMimeType = ct
		}
	}

	fileName := artifactFileName(artifact, fileModel.FileName, fileMimeType)
	fileReader := bytes.NewReader(respDownload.Body)
	width, height := u.imageDimensions(fileReader, fileMimeType)

	stored, err := u.storage.SavePersist(ctx, filestorage.SaveFileInput{
		Dir:      folderPathWithPreset,
		FileName: fileName,
		Size:     fileReader.Size(),
		MimeType: fileMimeType,
		Reader:   bytes.NewReader(respDownload.Body),
	})
	if err != nil {
		return fileStorageDTO{}, fmt.Errorf("commit file: %w", err)
	}

	return fileStorageDTO{
		RelativePath: stored.RelativePath,
		FolderPath:   filesanitize.EnsureRelativeDir(stored.RelativePath),
		FileName:     filepath.Base(stored.RelativePath),
		URL:          stored.URL,
		Size:         stored.Size,
		MimeType:     stored.MimeType,
		Width:        width,
		Height:       height,
	}, nil
}

func (u *UseCase) imageDimensions(fileRemote io.Reader, mime string) (width, height int) {
	if mime == "" || !isImage(mime) {
		return 0, 0
	}

	cfg, _, err := image.DecodeConfig(fileRemote)
	if err != nil {
		return 0, 0
	}

	return cfg.Width, cfg.Height
}

func isImage(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "image/"
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

func artifactFileName(artifact Artifact, fallback, detectedMime string) string {
	ext := selectExtension(detectedMime, artifact, fallback)

	// Prefer existing UUID-based names.
	if name := uuidBaseCandidate(metadataString(artifact.Metadata, "file_name")); name != "" {
		return appendExtension(name, ext)
	}
	if name := uuidBaseCandidate(artifactURLFileName(artifact.URL)); name != "" {
		return appendExtension(name, ext)
	}
	if name := uuidBaseCandidate(fallback); name != "" {
		return appendExtension(name, ext)
	}

	generated := uuid.New().String()
	if ext != "" {
		return generated + ext
	}

	return generated
}

func artifactURLFileName(rawURL string) string {
	if rawURL == "" {
		return ""
	}

	u, err := url.Parse(rawURL)
	if err != nil {
		return ""
	}

	name := path.Base(u.Path)
	if name == "" || name == "." || name == "/" {
		return ""
	}

	return name
}

func selectExtension(detectedMime string, artifact Artifact, fallback string) string {
	if ext := extensionFromContentType(detectedMime); ext != "" {
		return ext
	}
	if ext := extensionFromContentType(artifact.ContentType); ext != "" {
		return ext
	}
	if ext := extensionFromFileName(metadataString(artifact.Metadata, "file_name")); ext != "" {
		return ext
	}
	if ext := extensionFromFileName(artifactURLFileName(artifact.URL)); ext != "" {
		return ext
	}

	return extensionFromFileName(fallback)
}

func extensionFromFileName(name string) string {
	if name == "" {
		return ""
	}

	ext := strings.ToLower(path.Ext(strings.TrimSpace(name)))
	if ext == "" || ext == "." {
		return ""
	}

	return ext
}

func uuidBaseCandidate(candidate string) string {
	name := path.Base(strings.TrimSpace(candidate))
	if name == "" {
		return ""
	}

	base := strings.TrimSuffix(name, path.Ext(name))
	if base == "" {
		return ""
	}

	if _, err := uuid.Parse(base); err == nil {
		return base
	}

	return ""
}

func appendExtension(base, ext string) string {
	if base == "" {
		return ""
	}

	if ext == "" {
		return base
	}

	return base + ext
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
