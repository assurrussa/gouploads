package uploadservice

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"path/filepath"
	"strings"
	"time"
	"unicode/utf8"

	logger "github.com/assurrussa/gologger"
	"github.com/google/uuid"

	uploadconfig "github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/filepolicy"
	"github.com/assurrussa/gouploads/internal/filesanitize"
)

const (
	sniffLen                 = 512
	failedTempCleanupTimeout = 30 * time.Second
)

const (
	extJPG        = ".jpg"
	extJPEG       = ".jpeg"
	extPNG        = ".png"
	extGIF        = ".gif"
	extWebP       = ".webp"
	extMP4        = ".mp4"
	extWebM       = ".webm"
	extPDF        = ".pdf"
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
func (e *uploadError) Unwrap() error              { return e.err }
func (e *uploadError) Code() string               { return e.code }
func newUploadError(code string, err error) error { return &uploadError{code: code, err: err} }

type FileUploadConfig struct {
	MaxFileSize       int64
	AllowedExtensions []string
	AllowedMimeTypes  map[string][]string
	UploadDir         string
	SkipResizer       bool
}

// Clone isolates request-specific changes from a strategy's reusable policy.
func (c *FileUploadConfig) Clone() *FileUploadConfig {
	if c == nil {
		return nil
	}
	cloned := *c
	cloned.AllowedExtensions = append([]string(nil), c.AllowedExtensions...)
	cloned.AllowedMimeTypes = cloneMimeMap(c.AllowedMimeTypes)
	return &cloned
}

type UploadedFile struct {
	OriginalName string
	FileName     string
	Size         int64
	Path         string
	FolderPath   string
	URL          string
	MimeType     string
	FileType     model.FileType
	Width        int
	Height       int
}

type ReaderUploadInput struct {
	OriginalName string
	Size         int64
	Reader       io.Reader
}

type UploadValidationInput struct {
	OriginalFileName string
	MimeType         string
	Extension        string
	Size             int64
	Sniff            []byte
}

// UploadValidator checks metadata and a bounded prefix only. Full-content malware
// scanning belongs to a ContentScanner on the private finalization source.
type UploadValidator interface {
	Validate(ctx context.Context, input UploadValidationInput) error
}
type UploadValidatorFunc func(context.Context, UploadValidationInput) error

func (fn UploadValidatorFunc) Validate(ctx context.Context, input UploadValidationInput) error {
	return fn(ctx, input)
}

func DefaultFileUploadConfig(addPathDir ...string) *FileUploadConfig {
	return &FileUploadConfig{
		MaxFileSize:       10 * 1024 * 1024,
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
		UploadDir: strings.Join(append([]string{"upload_file"}, addPathDir...), "/"),
	}
}

func DefaultFileUploadRichTextConfig(addPathDir ...string) *FileUploadConfig {
	return &FileUploadConfig{
		MaxFileSize:       5 * 1024 * 1024,
		AllowedExtensions: []string{extJPG, extJPEG, extPNG, extGIF, extWebP},
		AllowedMimeTypes: map[string][]string{
			extJPG:  {mimeImageJPEG},
			extJPEG: {mimeImageJPEG},
			extPNG:  {mimeImagePNG},
			extGIF:  {mimeImageGIF},
			extWebP: {mimeImageWebP},
		},
		UploadDir: strings.Join(append([]string{"upload_file", "rich-text"}, addPathDir...), "/"),
	}
}

func (s *Service) ProcessSingleFileUpload(
	ctx context.Context,
	header *multipart.FileHeader,
	configs ...*FileUploadConfig,
) (UploadedFile, error) {
	cfg := s.mergeConfig(configs...)
	if cfg.UploadDir == "" {
		return UploadedFile{}, newUploadError(uploadErrorCodeUploadDirMissing, errors.New("upload directory is not defined"))
	}
	return s.processFile(ctx, header, cfg)
}

func (s *Service) ProcessReaderUpload(
	ctx context.Context,
	input ReaderUploadInput,
	configs ...*FileUploadConfig,
) (UploadedFile, error) {
	cfg := s.mergeConfig(configs...)
	if cfg.UploadDir == "" {
		return UploadedFile{}, newUploadError(uploadErrorCodeUploadDirMissing, errors.New("upload directory is not defined"))
	}
	return s.processReader(ctx, input, cfg)
}

func (s *Service) processFile(ctx context.Context, header *multipart.FileHeader, cfg *FileUploadConfig) (UploadedFile, error) {
	if header == nil {
		return UploadedFile{}, newUploadError(uploadErrorCodeFileHeaderMissing, shared.ErrFileHeaderIsNil)
	}
	if err := ensureDeclaredSize(header.Size, cfg.MaxFileSize); err != nil {
		return UploadedFile{}, err
	}
	if err := s.ensureAllowedExtension(strings.ToLower(filepath.Ext(header.Filename)), cfg.AllowedExtensions); err != nil {
		return UploadedFile{}, err
	}
	source, err := s.openMultipartFile(ctx, header)
	if err != nil {
		return UploadedFile{}, err
	}
	defer s.closeSource(ctx, source)
	return s.processReader(ctx, ReaderUploadInput{OriginalName: header.Filename, Size: header.Size, Reader: source}, cfg)
}

func (s *Service) processReader(ctx context.Context, input ReaderUploadInput, cfg *FileUploadConfig) (UploadedFile, error) {
	if err := ctx.Err(); err != nil {
		return UploadedFile{}, err
	}
	if input.Reader == nil {
		return UploadedFile{}, newUploadError(uploadErrorCodeFileHeaderMissing, shared.ErrFileHeaderIsNil)
	}
	name := strings.TrimSpace(input.OriginalName)
	if name == "" || !utf8.ValidString(name) || utf8.RuneCountInString(name) > 255 || strings.ContainsRune(name, 0) {
		return UploadedFile{}, newUploadError(uploadErrorCodeFileHeaderMissing, errors.New("invalid original file name"))
	}
	if err := ensureDeclaredSize(input.Size, cfg.MaxFileSize); err != nil {
		return UploadedFile{}, err
	}
	ext := strings.ToLower(filepath.Ext(name))
	if err := s.ensureAllowedExtension(ext, cfg.AllowedExtensions); err != nil {
		return UploadedFile{}, err
	}
	// Install the whole-file budget before MIME/dimension parsing.
	reader, limited := wrapWithSizeLimit(input.Reader, cfg.MaxFileSize)
	header := make([]byte, sniffLen)
	n, err := io.ReadFull(reader, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return UploadedFile{}, newUploadError(uploadErrorCodeReadFailed, err)
	}
	if n == 0 {
		return UploadedFile{}, newUploadError(uploadErrorCodeFileEmpty, errors.New("file is empty"))
	}
	mimeType := filepolicy.DetectContentType(header[:n])
	if !s.isAllowedMime(ext, mimeType, cfg.AllowedMimeTypes) {
		return UploadedFile{},
			newUploadError(uploadErrorCodeMimeDenied,
				fmt.Errorf("mime type %s is not allowed for %s",
					mimeType,
					ext))
	}
	reader = io.MultiReader(bytes.NewReader(header[:n]), reader)
	reader, err = filepolicy.InspectAudioHeader(reader, header[:n], mimeType, input.Size)
	if err != nil {
		return UploadedFile{}, newUploadError(uploadErrorCodeMimeDenied, err)
	}
	if err := s.runValidatorsFromInput(ctx, input, ext, mimeType, header[:n]); err != nil {
		return UploadedFile{}, err
	}
	reader, width, height := inspectImageDimensions(reader, mimeType)
	kind, err := model.GetFileTypeFromMimeType(mimeType)
	if err != nil {
		return UploadedFile{}, err
	}
	fileName := uuid.NewString() + ext
	if kind == model.FileTypeAudio && s.processingMode == uploadconfig.ProcessingMediaResizer {
		return UploadedFile{}, ClientError{Message: "audio uploads require original_only processing"}
	}
	temporary, err := s.saveTempFile(ctx, cfg, fileName, input.Size, mimeType, reader)
	if err != nil {
		return UploadedFile{}, err
	}
	key, err := filesanitize.EnsureRelativePath(temporary.RelativePath)
	if err != nil {
		return UploadedFile{}, err
	}
	if err := s.ensureStoredSize(ctx, temporary, input.Size, cfg.MaxFileSize, limited); err != nil {
		return UploadedFile{}, err
	}
	return UploadedFile{
		OriginalName: name,
		FileName:     fileName,
		Size:         temporary.Size,
		Path:         key,
		FolderPath:   filesanitize.EnsureRelativeDir(key),
		URL:          temporary.URL,
		MimeType:     mimeType,
		FileType:     kind,
		Width:        width,
		Height:       height,
	}, nil
}

func ensureDeclaredSize(size, limit int64) error {
	if size < 0 {
		return newUploadError(uploadErrorCodeReadFailed, errors.New("negative file size"))
	}
	if limit > 0 && size > limit {
		return newUploadError(uploadErrorCodeFileTooLarge, fmt.Errorf("file size %d exceeds maximum allowed size %d", size, limit))
	}
	return nil
}

func (s *Service) ensureAllowedExtension(ext string, allowed []string) error {
	if s.isAllowedExtension(ext, allowed) {
		return nil
	}
	return newUploadError(uploadErrorCodeExtensionDenied, fmt.Errorf("file extension %s is not allowed", ext))
}

func (s *Service) openMultipartFile(ctx context.Context, header *multipart.FileHeader) (multipart.File, error) {
	file, err := header.Open()
	if err != nil {
		s.logger.WarnContext(ctx, "upload: open file", logger.Error(err))
		return nil, newUploadError(uploadErrorCodeOpenFailed, err)
	}
	return file, nil
}

func (s *Service) closeSource(ctx context.Context, file multipart.File) {
	if err := file.Close(); err != nil {
		s.logger.ErrorContext(ctx, "failed to close uploaded source", logger.Error(err))
	}
}

func (s *Service) runValidatorsFromInput(ctx context.Context, input ReaderUploadInput, ext, mimeType string, sniff []byte) error {
	for _, validator := range s.validators {
		if validator == nil {
			continue
		}
		validation := UploadValidationInput{
			OriginalFileName: input.OriginalName,
			Size:             input.Size,
			Extension:        ext,
			MimeType:         mimeType,
			Sniff: append([]byte(nil),
				sniff...),
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
	// All runtime configurations are bounded; avoid overflow for direct callers.
	if limit > filepolicy.MaxFileSize {
		limit = filepolicy.MaxFileSize
	}
	limited := &io.LimitedReader{R: reader, N: limit + 1}
	return limited, limited
}

func (s *Service) saveTempFile(
	ctx context.Context,
	cfg *FileUploadConfig,
	name string,
	size int64,
	mimeType string,
	reader io.Reader,
) (filestorage.StoredFile, error) {
	file, err := s.storage.SaveTemp(ctx,
		filestorage.SaveFileInput{
			Dir:      cfg.UploadDir,
			FileName: name,
			Size:     size,
			MimeType: mimeType,
			Reader:   reader,
		})
	if err != nil {
		s.logger.WarnContext(ctx, "upload: save temp file", logger.Error(err))
		return filestorage.StoredFile{}, newUploadError(uploadErrorCodeSaveTempFailed, err)
	}
	return file, nil
}

func (s *Service) ensureStoredSize(
	ctx context.Context,
	file filestorage.StoredFile,
	declaredSize,
	limit int64,
	limited *io.LimitedReader,
) error {
	if limit <= 0 {
		return nil
	}
	size := file.Size
	if size == 0 {
		size = declaredSize
	}
	if (limited != nil && limited.N == 0) || size > limit {
		cleanupCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), failedTempCleanupTimeout)
		defer cancel()
		if err := s.storage.Delete(cleanupCtx, file.RelativePath); err != nil && !errors.Is(err, filestorage.ErrNotSupported) {
			s.logger.WarnContext(cleanupCtx, "upload: cleanup oversize file", logger.Error(err))
		}
		return newUploadError(uploadErrorCodeFileTooLarge, fmt.Errorf("file size %d exceeds maximum allowed size %d", size, limit))
	}
	return nil
}

func (s *Service) isAllowedExtension(ext string, allowed []string) bool {
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed {
		if strings.EqualFold(candidate, ext) {
			return true
		}
	}
	return false
}

func (s *Service) isAllowedMime(ext, mimeType string, allowed map[string][]string) bool {
	mimeType = filepolicy.NormalizeMIME(mimeType)
	if mimeType == "" {
		return false
	}
	if len(allowed) == 0 {
		return true
	}
	for _, candidate := range allowed[strings.ToLower(ext)] {
		candidate = strings.ToLower(strings.TrimSpace(candidate))
		if strings.HasSuffix(candidate, "/*") && strings.HasPrefix(mimeType, strings.TrimSuffix(candidate, "*")) {
			return true
		}
		if filepolicy.NormalizeMIME(candidate) == mimeType {
			return true
		}
	}
	return false
}

func (s *Service) mergeConfig(configs ...*FileUploadConfig) *FileUploadConfig {
	return ResolveFileUploadConfig(configs...)
}

// ResolveFileUploadConfig applies ingestion defaults and isolates mutable policy
// fields so transports can enforce the same limits before accepting bytes.
func ResolveFileUploadConfig(configs ...*FileUploadConfig) *FileUploadConfig {
	result := DefaultFileUploadConfig()
	for _, cfg := range configs {
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
	return result
}

func normalizeExtensions(extensions []string) []string {
	if len(extensions) == 0 {
		return nil
	}
	result := make([]string, 0, len(extensions))
	for _, ext := range extensions {
		ext = strings.ToLower(strings.TrimSpace(ext))
		if ext == "" {
			continue
		}
		if !strings.HasPrefix(ext, ".") {
			ext = "." + ext
		}
		result = append(result, ext)
	}
	return result
}

func inspectImageDimensions(reader io.Reader, mimeType string) (replay io.Reader, width, height int) {
	return filepolicy.InspectDimensions(reader, mimeType)
}
