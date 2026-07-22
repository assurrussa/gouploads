package uploadservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"image"
	"io"
	"mime/multipart"
	"net/http"
	"path/filepath"
	"strings"

	"github.com/assurrussa/goshared/pkg/filesanitize"
	commonshared "github.com/assurrussa/goshared/pkg/filetypes"
	"github.com/assurrussa/goshared/pkg/logger"
	"github.com/google/uuid"

	"github.com/assurrussa/gouploads/domain/files/shared"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

const sniffLen = 512

const (
	extJPG  = ".jpg"
	extJPEG = ".jpeg"
	extPNG  = ".png"
	extGIF  = ".gif"
	extWebP = ".webp"
	extMP4  = ".mp4"
	extWebM = ".webm"
	extPDF  = ".pdf"

	mimeImageJPEG = "image/jpeg"
	mimeImagePNG  = "image/png"
	mimeImageGIF  = "image/gif"
	mimeImageWebP = "image/webp"
	mimeVideoMP4  = "video/mp4"
	mimeVideoWebM = "video/webm"
	mimePDF       = "application/pdf"
)

const (
	uploadErrorCodeFileHeaderMissing = "file_header_missing"
	uploadErrorCodeFileTooLarge      = "file_too_large"
	uploadErrorCodeExtensionDenied   = "extension_denied"
	uploadErrorCodeMimeDenied        = "mime_denied"
	uploadErrorCodeFileEmpty         = "file_empty"
	uploadErrorCodeReadFailed        = "read_failed"
	uploadErrorCodeOpenFailed        = "open_failed"
	uploadErrorCodeSaveTempFailed    = "save_temp_failed"
	uploadErrorCodeUploadDirMissing  = "upload_dir_missing"
	uploadErrorCodeUnexpected        = "unexpected"
)

var uploadErrorMessages = map[string]string{
	uploadErrorCodeFileHeaderMissing: "Не найден файл для обработки",
	uploadErrorCodeFileTooLarge:      "Превышен допустимый размер файла",
	uploadErrorCodeExtensionDenied:   "Недопустимое расширение файла",
	uploadErrorCodeMimeDenied:        "Недопустимый тип содержимого файла",
	uploadErrorCodeFileEmpty:         "Файл пустой",
	uploadErrorCodeReadFailed:        "Не удалось прочитать файл",
	uploadErrorCodeOpenFailed:        "Не удалось открыть файл",
	uploadErrorCodeSaveTempFailed:    "Не удалось сохранить файл",
	uploadErrorCodeUploadDirMissing:  "Каталог загрузки не настроен",
	uploadErrorCodeUnexpected:        "Не удалось обработать файл",
}

type uploadError struct {
	code string
	err  error
}

func (e *uploadError) Error() string {
	if msg, ok := uploadErrorMessages[e.code]; ok {
		return msg
	}
	return uploadErrorMessages[uploadErrorCodeUnexpected]
}

func (e *uploadError) Unwrap() error {
	return e.err
}

func (e *uploadError) Code() string {
	return e.code
}

func newUploadError(code string, err error) error {
	return &uploadError{code: code, err: err}
}

// FileUploadConfig configures file upload behavior.
type FileUploadConfig struct {
	MaxFileSize       int64
	AllowedExtensions []string
	AllowedMimeTypes  map[string][]string
	UploadDir         string
	SkipResizer       bool
}

// UploadedFile represents an uploaded file with metadata.
type UploadedFile struct {
	OriginalName string
	FileName     string
	Size         int64
	Path         string
	FolderPath   string
	URL          string
	MimeType     string
	FileType     commonshared.FileType
	Width        int
	Height       int
}

// ReaderUploadInput describes a file provided via a reader (e.g. TUS).
type ReaderUploadInput struct {
	OriginalName string
	Size         int64
	Reader       io.Reader
}

// UploadValidationInput contains data available for custom upload validators.
type UploadValidationInput struct {
	OriginalFileName string
	MimeType         string
	Extension        string
	Size             int64
	Sniff            []byte
}

// UploadValidator allows plugging additional validation rules (e.g. antivirus).
type UploadValidator interface {
	Validate(ctx context.Context, input UploadValidationInput) error
}

// UploadValidatorFunc turns a function into an UploadValidator.
type UploadValidatorFunc func(ctx context.Context, input UploadValidationInput) error

func (fn UploadValidatorFunc) Validate(ctx context.Context, input UploadValidationInput) error {
	return fn(ctx, input)
}

// DefaultFileUploadConfig returns default configuration for file upload_file.
func DefaultFileUploadConfig(addPathDir ...string) *FileUploadConfig {
	uploadSegments := append([]string{"upload_file"}, addPathDir...)
	uploadDir := strings.Join(uploadSegments, "/")
	return &FileUploadConfig{
		MaxFileSize:       10 * 1024 * 1024, // 10MB
		AllowedExtensions: []string{extJPG, extJPEG, extPNG, extGIF, extWebP, extMP4, extWebM, extPDF},
		AllowedMimeTypes: map[string][]string{
			extJPG:  {mimeImageJPEG},
			extJPEG: {mimeImageJPEG},
			extPNG:  {mimeImagePNG},
			extGIF:  {mimeImageGIF},
			extWebP: {mimeImageWebP},
			extMP4:  {mimeVideoMP4},
			extWebM: {mimeVideoWebM},
			extPDF:  {mimePDF},
		},
		UploadDir: uploadDir,
	}
}

// DefaultFileUploadRichTextConfig returns default configuration for rich text editor file upload_file.
func DefaultFileUploadRichTextConfig(addPathDir ...string) *FileUploadConfig {
	segments := append([]string{"upload_file", "rich-text"}, addPathDir...)
	uploadDir := strings.Join(segments, "/")
	return &FileUploadConfig{
		MaxFileSize:       5 * 1024 * 1024, // 5MB для Rich Text
		AllowedExtensions: []string{extJPG, extJPEG, extPNG, extGIF, extWebP},
		AllowedMimeTypes: map[string][]string{
			extJPG:  {mimeImageJPEG},
			extJPEG: {mimeImageJPEG},
			extPNG:  {mimeImagePNG},
			extGIF:  {mimeImageGIF},
			extWebP: {mimeImageWebP},
		},
		UploadDir: uploadDir,
	}
}

// ProcessSingleFileUpload processes a single file upload.
func (s *Service) ProcessSingleFileUpload(
	ctx context.Context,
	fileHeader *multipart.FileHeader,
	configs ...*FileUploadConfig,
) (UploadedFile, error) {
	config := s.mergeConfig(configs...)

	if config.UploadDir == "" {
		return UploadedFile{}, newUploadError(uploadErrorCodeUploadDirMissing, errors.New("upload directory is not defined"))
	}

	return s.processFile(ctx, fileHeader, config)
}

// ProcessReaderUpload processes a file upload from a reader.
func (s *Service) ProcessReaderUpload(
	ctx context.Context,
	input ReaderUploadInput,
	configs ...*FileUploadConfig,
) (UploadedFile, error) {
	config := s.mergeConfig(configs...)

	if config.UploadDir == "" {
		return UploadedFile{}, newUploadError(uploadErrorCodeUploadDirMissing, errors.New("upload directory is not defined"))
	}

	return s.processReader(ctx, input, config)
}

// processFile handles the processing of a single file.
func (s *Service) processFile(
	ctx context.Context, fileHeader *multipart.FileHeader, config *FileUploadConfig,
) (UploadedFile, error) {
	if fileHeader == nil {
		err := shared.ErrFileHeaderIsNil
		s.logger.WarnContext(ctx, "upload: missing file header", logger.Error(err))
		return UploadedFile{}, newUploadError(uploadErrorCodeFileHeaderMissing, err)
	}

	if err := ensureDeclaredSize(fileHeader.Size, config.MaxFileSize); err != nil {
		return UploadedFile{}, err
	}

	ext := strings.ToLower(filepath.Ext(fileHeader.Filename))
	if err := s.ensureAllowedExtension(ext, config.AllowedExtensions); err != nil {
		return UploadedFile{}, err
	}

	src, err := s.openMultipartFile(ctx, fileHeader)
	if err != nil {
		return UploadedFile{}, err
	}
	defer s.closeSource(ctx, src)

	reader, mimeType, err := s.prepareReader(ctx, fileHeader, src, ext, config)
	if err != nil {
		return UploadedFile{}, err
	}

	fileType, err := commonshared.GetFileTypeFromMimeType(mimeType)
	if err != nil {
		return UploadedFile{}, err
	}

	reader, width, height := inspectImageDimensions(reader, mimeType)
	reader, limited := wrapWithSizeLimit(reader, config.MaxFileSize)
	fileName := uuid.New().String() + ext

	tempFile, err := s.saveTempFile(ctx, config, fileName, fileHeader.Size, mimeType, reader)
	if err != nil {
		return UploadedFile{}, err
	}

	relativePath, err := filesanitize.EnsureRelativePath(tempFile.RelativePath)
	if err != nil {
		return UploadedFile{}, err
	}

	if err := s.ensureStoredSize(ctx, tempFile, fileHeader.Size, config.MaxFileSize, limited); err != nil {
		return UploadedFile{}, err
	}

	return UploadedFile{
		OriginalName: fileHeader.Filename,
		FileName:     fileName,
		Size:         tempFile.Size,
		Path:         relativePath,
		FolderPath:   filesanitize.EnsureRelativeDir(relativePath),
		URL:          tempFile.URL,
		MimeType:     mimeType,
		FileType:     fileType,
		Width:        width,
		Height:       height,
	}, nil
}

// processReader handles the processing of a single file from reader.
func (s *Service) processReader(
	ctx context.Context,
	input ReaderUploadInput,
	config *FileUploadConfig,
) (UploadedFile, error) {
	if input.Reader == nil {
		err := shared.ErrFileHeaderIsNil
		s.logger.WarnContext(ctx, "upload: missing reader", logger.Error(err))
		return UploadedFile{}, newUploadError(uploadErrorCodeFileHeaderMissing, err)
	}

	originalName := strings.TrimSpace(input.OriginalName)
	if originalName == "" {
		err := errors.New("original file name is required")
		s.logger.WarnContext(ctx, "upload: missing file name", logger.Error(err))
		return UploadedFile{}, newUploadError(uploadErrorCodeFileHeaderMissing, err)
	}

	if err := ensureDeclaredSize(input.Size, config.MaxFileSize); err != nil {
		return UploadedFile{}, err
	}

	ext := strings.ToLower(filepath.Ext(originalName))
	if err := s.ensureAllowedExtension(ext, config.AllowedExtensions); err != nil {
		return UploadedFile{}, err
	}

	sniff := make([]byte, sniffLen)
	n, readErr := io.ReadFull(input.Reader, sniff)
	if readErr != nil && readErr != io.EOF && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		s.logger.WarnContext(ctx, "upload: read file", logger.Error(readErr))
		return UploadedFile{}, newUploadError(
			uploadErrorCodeReadFailed,
			fmt.Errorf("failed to read file for mime detection: %w", readErr),
		)
	}
	if n == 0 {
		return UploadedFile{}, newUploadError(uploadErrorCodeFileEmpty, errors.New("file is empty"))
	}

	data := sniff[:n]
	mimeType := http.DetectContentType(data)
	if !s.isAllowedMime(ext, mimeType, config.AllowedMimeTypes) {
		err := fmt.Errorf("mime type %s is not allowed for %s", mimeType, ext)
		return UploadedFile{}, newUploadError(uploadErrorCodeMimeDenied, err)
	}

	if err := s.runValidatorsFromInput(ctx, input, ext, mimeType, data); err != nil {
		return UploadedFile{}, err
	}

	reader := io.MultiReader(bytes.NewReader(data), input.Reader)

	fileType, err := commonshared.GetFileTypeFromMimeType(mimeType)
	if err != nil {
		return UploadedFile{}, err
	}

	reader, width, height := inspectImageDimensions(reader, mimeType)
	reader, limited := wrapWithSizeLimit(reader, config.MaxFileSize)
	fileName := uuid.New().String() + ext

	tempFile, err := s.saveTempFile(ctx, config, fileName, input.Size, mimeType, reader)
	if err != nil {
		return UploadedFile{}, err
	}

	relativePath, err := filesanitize.EnsureRelativePath(tempFile.RelativePath)
	if err != nil {
		return UploadedFile{}, err
	}

	if err := s.ensureStoredSize(ctx, tempFile, input.Size, config.MaxFileSize, limited); err != nil {
		return UploadedFile{}, err
	}

	return UploadedFile{
		OriginalName: originalName,
		FileName:     fileName,
		Size:         tempFile.Size,
		Path:         relativePath,
		FolderPath:   filesanitize.EnsureRelativeDir(relativePath),
		URL:          tempFile.URL,
		MimeType:     mimeType,
		FileType:     fileType,
		Width:        width,
		Height:       height,
	}, nil
}

func ensureDeclaredSize(size, limit int64) error {
	if limit > 0 && size > limit {
		err := fmt.Errorf("file size %d exceeds maximum allowed size %d", size, limit)
		return newUploadError(uploadErrorCodeFileTooLarge, err)
	}
	return nil
}

func (s *Service) ensureAllowedExtension(ext string, allowed []string) error {
	if s.isAllowedExtension(ext, allowed) {
		return nil
	}
	err := fmt.Errorf("file extension %s is not allowed", ext)
	return newUploadError(uploadErrorCodeExtensionDenied, err)
}

func (s *Service) openMultipartFile(ctx context.Context, fileHeader *multipart.FileHeader) (multipart.File, error) {
	src, err := fileHeader.Open()
	if err != nil {
		s.logger.WarnContext(ctx, "upload: open file", logger.Error(err))
		return nil, newUploadError(
			uploadErrorCodeOpenFailed,
			fmt.Errorf("failed to open uploaded file: %w", err),
		)
	}
	return src, nil
}

func (s *Service) closeSource(ctx context.Context, src multipart.File) {
	if closeErr := src.Close(); closeErr != nil {
		s.logger.ErrorContext(ctx, "failed to close uploaded file in src", logger.Error(closeErr))
	}
}

func (s *Service) prepareReader(
	ctx context.Context,
	fileHeader *multipart.FileHeader,
	src multipart.File,
	ext string,
	config *FileUploadConfig,
) (io.Reader, string, error) {
	sniff := make([]byte, sniffLen)
	n, readErr := io.ReadFull(src, sniff)
	if readErr != nil && readErr != io.EOF && !errors.Is(readErr, io.ErrUnexpectedEOF) {
		s.logger.WarnContext(ctx, "upload: read file", logger.Error(readErr))
		return nil, "", newUploadError(
			uploadErrorCodeReadFailed,
			fmt.Errorf("failed to read file for mime detection: %w", readErr),
		)
	}
	if n == 0 {
		return nil, "", newUploadError(uploadErrorCodeFileEmpty, errors.New("file is empty"))
	}

	data := sniff[:n]
	mimeType := http.DetectContentType(data)
	if !s.isAllowedMime(ext, mimeType, config.AllowedMimeTypes) {
		err := fmt.Errorf("mime type %s is not allowed for %s", mimeType, ext)
		return nil, "", newUploadError(uploadErrorCodeMimeDenied, err)
	}

	if err := s.runValidators(ctx, fileHeader, ext, mimeType, data); err != nil {
		return nil, "", err
	}

	reader := io.MultiReader(bytes.NewReader(data), src)
	return reader, mimeType, nil
}

func (s *Service) runValidators(
	ctx context.Context,
	fileHeader *multipart.FileHeader,
	ext string,
	mimeType string,
	sniff []byte,
) error {
	if len(s.validators) == 0 {
		return nil
	}

	input := UploadValidationInput{
		OriginalFileName: fileHeader.Filename,
		MimeType:         mimeType,
		Extension:        ext,
		Size:             fileHeader.Size,
		Sniff:            append([]byte(nil), sniff...),
	}

	for _, validator := range s.validators {
		if validator == nil {
			continue
		}
		if err := validator.Validate(ctx, input); err != nil {
			var uploadErr *uploadError
			if errors.As(err, &uploadErr) {
				return err
			}
			return newUploadError(uploadErrorCodeUnexpected, err)
		}
	}

	return nil
}

func (s *Service) runValidatorsFromInput(
	ctx context.Context,
	input ReaderUploadInput,
	ext string,
	mimeType string,
	sniff []byte,
) error {
	if len(s.validators) == 0 {
		return nil
	}

	validation := UploadValidationInput{
		OriginalFileName: input.OriginalName,
		MimeType:         mimeType,
		Extension:        ext,
		Size:             input.Size,
		Sniff:            append([]byte(nil), sniff...),
	}

	for _, validator := range s.validators {
		if validator == nil {
			continue
		}
		if err := validator.Validate(ctx, validation); err != nil {
			var uploadErr *uploadError
			if errors.As(err, &uploadErr) {
				return err
			}
			return newUploadError(uploadErrorCodeUnexpected, err)
		}
	}

	return nil
}

func wrapWithSizeLimit(reader io.Reader, limit int64) (io.Reader, *io.LimitedReader) {
	if limit <= 0 {
		return reader, nil
	}
	limited := &io.LimitedReader{R: reader, N: limit + 1}
	return limited, limited
}

func (s *Service) saveTempFile(
	ctx context.Context,
	config *FileUploadConfig,
	fileName string,
	declaredSize int64,
	mimeType string,
	reader io.Reader,
) (filestorage.StoredFile, error) {
	tempFile, err := s.storage.SaveTemp(ctx, filestorage.SaveFileInput{
		Dir:      config.UploadDir,
		FileName: fileName,
		Size:     declaredSize,
		MimeType: mimeType,
		Reader:   reader,
	})
	if err != nil {
		s.logger.WarnContext(ctx, "upload: save temp file", logger.Error(err))
		return filestorage.StoredFile{}, newUploadError(uploadErrorCodeSaveTempFailed, fmt.Errorf("save temp file: %w", err))
	}
	return tempFile, nil
}

func (s *Service) ensureStoredSize(
	ctx context.Context,
	tempFile filestorage.StoredFile,
	declaredSize int64,
	limit int64,
	limited *io.LimitedReader,
) error {
	if limit <= 0 {
		return nil
	}

	effectiveSize := tempFile.Size
	if effectiveSize == 0 {
		effectiveSize = declaredSize
	}

	if (limited != nil && limited.N == 0) || effectiveSize > limit {
		if err := s.storage.Delete(ctx, tempFile.RelativePath); err != nil && !errors.Is(err, filestorage.ErrNotSupported) {
			s.logger.WarnContext(ctx, "upload: cleanup oversize temp file", logger.Error(err))
		}
		err := fmt.Errorf("file size %d exceeds maximum allowed size %d", effectiveSize, limit)
		return newUploadError(uploadErrorCodeFileTooLarge, err)
	}

	return nil
}

// isAllowedExtension checks if the file extension is allowed.
func (s *Service) isAllowedExtension(ext string, allowedExtensions []string) bool {
	ext = strings.ToLower(ext)
	if len(allowedExtensions) == 0 {
		return true
	}

	for _, allowed := range allowedExtensions {
		if strings.ToLower(allowed) == ext {
			return true
		}
	}
	return false
}

func (s *Service) isAllowedMime(ext string, mime string, allowed map[string][]string) bool {
	ext = strings.ToLower(ext)
	mime = strings.ToLower(strings.TrimSpace(mime))
	if idx := strings.Index(mime, ";"); idx >= 0 {
		mime = strings.TrimSpace(mime[:idx])
	}
	if mime == "" {
		return false
	}

	if len(allowed) == 0 {
		return true
	}

	allowedList, ok := allowed[ext]
	if !ok {
		return false
	}

	for _, candidate := range allowedList {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if idx := strings.Index(candidate, ";"); idx >= 0 {
			candidate = strings.TrimSpace(candidate[:idx])
		}
		if candidate == "" {
			continue
		}
		if strings.HasSuffix(candidate, "/*") {
			prefix := strings.TrimSuffix(candidate, "/*")
			if strings.HasPrefix(mime, prefix) {
				return true
			}
			continue
		}
		if candidate == mime {
			return true
		}
	}

	return false
}

func (s *Service) mergeConfig(cfgList ...*FileUploadConfig) *FileUploadConfig {
	base := DefaultFileUploadConfig()
	result := FileUploadConfig{
		MaxFileSize:       base.MaxFileSize,
		AllowedExtensions: append([]string(nil), base.AllowedExtensions...),
		AllowedMimeTypes:  cloneMimeMap(base.AllowedMimeTypes),
		UploadDir:         base.UploadDir,
		SkipResizer:       base.SkipResizer,
	}

	for _, cfg := range cfgList {
		if cfg == nil {
			continue
		}
		if cfg.MaxFileSize > 0 {
			result.MaxFileSize = cfg.MaxFileSize
		}
		if len(cfg.AllowedExtensions) > 0 {
			result.AllowedExtensions = normalizeExtensions(cfg.AllowedExtensions)
		}
		if len(cfg.AllowedMimeTypes) > 0 {
			result.AllowedMimeTypes = cloneMimeMap(cfg.AllowedMimeTypes)
		}
		if cfg.UploadDir != "" {
			result.UploadDir = cfg.UploadDir
		}
		if cfg.SkipResizer {
			result.SkipResizer = true
		}
	}

	return &result
}

func normalizeExtensions(exts []string) []string {
	if len(exts) == 0 {
		return nil
	}
	result := make([]string, 0, len(exts))
	for _, ext := range exts {
		clean := strings.ToLower(strings.TrimSpace(ext))
		if clean == "" {
			continue
		}
		if !strings.HasPrefix(clean, ".") {
			clean = "." + clean
		}
		result = append(result, clean)
	}
	return result
}

func inspectImageDimensions(reader io.Reader, mime string) (replay io.Reader, width, height int) {
	if mime == "" || !isImage(mime) {
		return reader, 0, 0
	}

	var consumed bytes.Buffer
	cfg, _, err := image.DecodeConfig(io.TeeReader(reader, &consumed))
	replay = io.MultiReader(bytes.NewReader(consumed.Bytes()), reader)
	if err != nil {
		return replay, 0, 0
	}

	return replay, cfg.Width, cfg.Height
}

func isImage(mime string) bool {
	return len(mime) >= 6 && mime[:6] == "image/"
}
