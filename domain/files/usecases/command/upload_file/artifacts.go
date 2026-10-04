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
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/google/uuid"

	"github.com/assurrussa/gouploads/domain/files/model"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/filepolicy"
	"github.com/assurrussa/gouploads/internal/filesanitize"
)

type lockingRepository interface {
	GetByIDForUpdate(ctx context.Context, fileID int64) (model.File, error)
}

func (u *UseCase) processFileModel(ctx context.Context, req Request, file model.File) (model.File, []string, error) {
	mainIndex, err := validateArtifactPresets(req.Artifacts)
	if err != nil {
		return file, nil, err
	}
	data := file.GetData()
	// All outputs belong to the original processing job, even when a video
	// produces image previews. Capture the origin before the main artifact can
	// change the record's content type (for example, a video-to-GIF preset).
	originMediaType := file.FileType.ToString()
	presets := make(map[shared.PresetName]shared.FilePreset, len(req.Artifacts))
	paths := make([]string, 0, len(req.Artifacts))
	for index, artifact := range req.Artifacts {
		if artifact.ExpireAt.Before(time.Now()) {
			return file, paths, fmt.Errorf("artifact %q is expired", artifact.Preset)
		}
		stored, err := u.saveFileStorage(ctx, artifact, file, originMediaType)
		if stored.RelativePath != "" {
			paths = append(paths, stored.RelativePath)
		}
		if err != nil {
			return file, paths, fmt.Errorf("prepare storage: %w", err)
		}
		if index == mainIndex {
			if err := u.updateMainArtifact(&file, data, stored, artifact.Metadata); err != nil {
				return file, paths, err
			}
		}
		lower := strings.ToLower(artifact.Preset)
		presets[shared.PresetName(artifact.Preset)] = shared.FilePreset{
			PresetName:     artifact.Preset,
			Size:           stored.Size,
			MimeType:       stored.MimeType,
			Width:          stored.Width,
			Height:         stored.Height,
			RelativePath:   stored.RelativePath,
			ChecksumSHA256: stored.Checksum,
			MediaType:      artifact.MediaType,
			IsPreview:      metadataBool(artifact.Metadata, "preview") || strings.Contains(lower, "preview"),
			IsThumbnail:    metadataBool(artifact.Metadata, "thumbnail") || strings.Contains(lower, "thumbnail"),
		}
	}
	data.Presets = presets
	data.Uploader = shared.FileUploader{}
	file.SetData(data)
	return file, paths, nil
}

func validateArtifactPresets(artifacts []Artifact) (int, error) {
	if len(artifacts) == 0 || len(artifacts) > 128 {
		return -1, errors.New("finalization requires between 1 and 128 artifacts")
	}
	mainIndex := -1
	seen := make(map[string]struct{}, len(artifacts))
	for index, artifact := range artifacts {
		preset, err := filesanitize.SanitizeSegment(artifact.Preset)
		if err != nil {
			return -1, fmt.Errorf("sanitize artifact preset: %w", err)
		}
		if _, ok := seen[preset]; ok {
			return -1, fmt.Errorf("duplicate artifact preset %q", artifact.Preset)
		}
		seen[preset] = struct{}{}
		if artifact.Preset == shared.FilePresetMainName.String() && isMainArtifact(artifact) {
			mainIndex = index
		}
	}
	if mainIndex < 0 {
		for index, artifact := range artifacts {
			if isMainArtifact(artifact) {
				if mainIndex >= 0 {
					return -1, errors.New("ambiguous main video artifact; use the main preset")
				}
				mainIndex = index
			}
		}
	}
	if mainIndex < 0 {
		return -1, errors.New("finalization has no main artifact")
	}
	return mainIndex, nil
}

func (u *UseCase) updateMainArtifact(
	file *model.File, data *model.FileData, stored fileStorageDTO, metadata map[string]any,
) error {
	file.FolderPath, file.FileName = stored.FolderPath, stored.FileName
	file.Size, file.MimeType, file.URL = stored.Size, stored.MimeType, ""
	width, height := stored.Width, stored.Height
	if width <= 0 {
		width = metadataInt(metadata, "target_width")
	}
	if height <= 0 {
		height = metadataInt(metadata, "target_height")
	}
	if width > 0 {
		data.Width = width
	}
	if height > 0 {
		data.Height = height
	}
	kind, err := model.GetFileTypeFromMimeType(stored.MimeType)
	if err != nil {
		return fmt.Errorf("prepare mime type for file type: %w", err)
	}
	file.FileType = kind
	return nil
}

func (u *UseCase) saveFileStorage(
	ctx context.Context, artifact Artifact, file model.File, originMediaType string,
) (fileStorageDTO, error) {
	response, err := u.resizeClient.DownloadFile(ctx, clientresizer.RequestDownload{
		Preset:    artifact.Preset,
		URL:       artifact.URL,
		TypeMedia: originMediaType,
	})
	if err != nil {
		return fileStorageDTO{}, fmt.Errorf("download file resize preset: %w", err)
	}
	if response.Body == nil {
		return fileStorageDTO{}, errors.New("artifact source returned a nil reader")
	}
	defer response.Body.Close()
	if artifact.Size < 0 || artifact.Size > maxArtifactSize || response.ContentLength > maxArtifactSize {
		return fileStorageDTO{}, fmt.Errorf("artifact exceeds maximum size %d", maxArtifactSize)
	}
	if artifact.Size > 0 && response.ContentLength > 0 && artifact.Size != response.ContentLength {
		return fileStorageDTO{}, errors.New("artifact declared size mismatch")
	}
	reader := bufio.NewReaderSize(response.Body, artifactSniffSize)
	header, err := reader.Peek(artifactSniffSize)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, bufio.ErrBufferFull) {
		return fileStorageDTO{}, fmt.Errorf("inspect artifact body: %w", err)
	}
	if len(header) == 0 {
		return fileStorageDTO{}, errors.New("artifact body is empty")
	}
	contentType, err := validateArtifactContentType(artifact.ContentType, response.ContentType, http.DetectContentType(header))
	if err != nil {
		return fileStorageDTO{}, err
	}
	directory, err := u.finalArtifactDir(file)
	if err != nil {
		return fileStorageDTO{}, err
	}
	name, err := artifactPresetFileName(artifact, contentType)
	if err != nil {
		return fileStorageDTO{}, err
	}
	expected := artifact.Size
	if expected <= 0 && response.ContentLength > 0 {
		expected = response.ContentLength
	}
	var source io.Reader = reader
	if u.scanner != nil {
		spool, err := u.scanArtifact(ctx, source, expected, file.OriginalFileName, contentType)
		if err != nil {
			return fileStorageDTO{}, err
		}
		defer func() { _ = spool.Close(); _ = os.Remove(spool.Name()) }()
		source = spool
	}
	hasher := sha256.New()
	counter := &artifactReader{Reader: io.TeeReader(filepolicy.ExactReader(ctx, source, expected, maxArtifactSize), hasher)}
	stored, err := u.storage.SavePersist(ctx, filestorage.SaveFileInput{
		Dir:      directory,
		FileName: name,
		Size:     expected,
		MimeType: contentType,
		Reader:   counter,
	})
	if err != nil {
		return fileStorageDTO{}, fmt.Errorf("commit file: %w", err)
	}
	result := fileStorageDTO{
		RelativePath: stored.RelativePath,
		FolderPath:   filesanitize.EnsureRelativeDir(stored.RelativePath),
		FileName:     filepath.Base(stored.RelativePath),
		Size:         counter.Size(),
		MimeType:     contentType,
		Width:        metadataInt(artifact.Metadata, "target_width"),
		Height:       metadataInt(artifact.Metadata, "target_height"),
		Checksum:     hex.EncodeToString(hasher.Sum(nil)),
	}
	if result.Size > maxArtifactSize || result.Size <= 0 {
		return result, errors.New("stored artifact size is outside supported limits")
	}
	if expected > 0 && result.Size != expected {
		return result, fmt.Errorf("artifact size mismatch: expected=%d actual=%d", expected, result.Size)
	}
	if stored.Size > 0 && stored.Size != result.Size {
		return result, fmt.Errorf("stored artifact size mismatch: expected=%d actual=%d", result.Size, stored.Size)
	}
	return result, nil
}

// scanArtifact approves the same private bytes subsequently passed to storage.
func (u *UseCase) scanArtifact(
	ctx context.Context, source io.Reader, expected int64, name, contentType string,
) (*os.File, error) {
	spool, err := os.CreateTemp("", "gouploads-private-scan-*")
	if err != nil {
		return nil, fmt.Errorf("create scan spool: %w", err)
	}
	succeeded := false
	defer func() {
		if !succeeded {
			_ = spool.Close()
			_ = os.Remove(spool.Name())
		}
	}()
	size, err := io.Copy(spool, filepolicy.ExactReader(ctx, source, expected, maxArtifactSize))
	if err != nil {
		return nil, fmt.Errorf("spool artifact: %w", err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	metadata := filepolicy.Metadata{OriginalName: name, ContentType: contentType, Size: size}
	if err := u.scanner.Scan(ctx, filepolicy.ExactReader(ctx, spool, size, maxArtifactSize), metadata); err != nil {
		return nil, fmt.Errorf("scan artifact: %w", err)
	}
	if _, err := spool.Seek(0, io.SeekStart); err != nil {
		return nil, err
	}
	succeeded = true
	return spool, nil
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
func (r *artifactReader) Size() int64 { return r.n }
func isMainArtifact(artifact Artifact) bool {
	if metadataBool(artifact.Metadata, "preview") || metadataBool(artifact.Metadata, "thumbnail") {
		return false
	}
	return artifact.Preset == shared.FilePresetMainName.String() || isVideoArtifact(artifact)
}

func isVideoArtifact(artifact Artifact) bool {
	contentType := strings.ToLower(artifact.ContentType)
	if strings.HasPrefix(contentType, "image/") {
		return false
	}
	return strings.HasPrefix(contentType, "video/") ||
		strings.EqualFold(strings.TrimSpace(artifact.MediaType), "video") ||
		strings.EqualFold(metadataString(artifact.Metadata, "media_type"), "video")
}

func metadataBool(meta map[string]any, key string) bool {
	switch value := meta[key].(type) {
	case bool:
		return value
	case string:
		result, err := strconv.ParseBool(value)
		return err == nil && result
	case float64:
		return value != 0
	case int:
		return value != 0
	case int64:
		return value != 0
	case json.Number:
		result, err := strconv.ParseBool(value.String())
		return err == nil && result
	default:
		return false
	}
}

func metadataInt(meta map[string]any, key string) int {
	switch value := meta[key].(type) {
	case int:
		return value
	case int64:
		return int(value)
	case float64:
		return int(value)
	case string:
		result, err := strconv.Atoi(value)
		if err == nil {
			return result
		}
	case json.Number:
		result, err := value.Int64()
		if err == nil {
			return int(result)
		}
	}
	return 0
}

func metadataString(meta map[string]any, key string) string {
	switch value := meta[key].(type) {
	case string:
		return value
	case fmt.Stringer:
		return value.String()
	default:
		return ""
	}
}

func (u *UseCase) finalArtifactDir(file model.File) (string, error) {
	if err := file.ObjectType.Validate(); err != nil {
		return "", fmt.Errorf("validate final object type: %w", err)
	}
	if file.ObjectID == nil || file.ObjectID.Int64() <= 0 {
		return "", errors.New("final object id is required")
	}
	if _, err := uuid.Parse(file.Slug); err != nil {
		return "", fmt.Errorf("validate final file slug: %w", err)
	}
	segments := append(strings.Split(strings.Trim(u.baseFolder, "/"), "/"),
		file.ObjectType.String(), file.ObjectID.String(), file.Slug)
	return filesanitize.BuildSafePath(segments...)
}

func (u *UseCase) publicURL(key string) string { return fileurl.Compose(u.deliveryBaseURL, "", key) }

func artifactPresetFileName(artifact Artifact, contentType string) (string, error) {
	preset, err := filesanitize.SanitizeSegment(artifact.Preset)
	if err != nil {
		return "", fmt.Errorf("sanitize artifact preset: %w", err)
	}
	ext := extensionFromContentType(contentType)
	if ext == "" {
		return "", fmt.Errorf("artifact content type %s has no supported extension", contentType)
	}
	return filesanitize.SanitizeFileName(preset + ext)
}

func validateArtifactContentType(webhookType, responseType, detectedType string) (string, error) {
	selected := ""
	for _, raw := range []string{webhookType, responseType, detectedType} {
		value := normalizeContentType(raw)
		if value == "" || value == "application/octet-stream" {
			continue
		}
		if selected == "" {
			selected = value
			continue
		}
		if value != selected {
			return "", fmt.Errorf("artifact content type mismatch: %s != %s", selected, value)
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
func allowedArtifactContentType(contentType string) bool { return filepolicy.Allowed(contentType) }
func normalizeContentType(raw string) string             { return filepolicy.NormalizeMIME(raw) }
func extensionFromContentType(contentType string) string { return filepolicy.Extension(contentType) }
