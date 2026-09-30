package uploadservice

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"path/filepath"
	"strings"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/internal/filepolicy"
	"github.com/assurrussa/gouploads/internal/filesanitize"
)

// BatchError identifies the first failed entry; the returned files are the
// successful prefix. Unwrap preserves ClientError/ValidationError classification.
type BatchError struct {
	FailedIndex int
	Err         error
}

func (e *BatchError) Error() string {
	return fmt.Sprintf("upload file [%d idx]: %v", e.FailedIndex, e.Err)
}
func (e *BatchError) Unwrap() error { return e.Err }

type finalizationRepository interface {
	FinalizeUpload(
		ctx context.Context, key, binding string, stored bool, create func(context.Context) (model.File, error),
	) (model.File, error)
}

func (s *Service) withFinalization(
	ctx context.Context,
	req ReaderRequest,
	name string,
	size int64,
	stored bool,
	create func(context.Context) (model.File, error),
) (model.File, error) {
	if err := req.Validate(); err != nil {
		return model.File{}, fmt.Errorf("validate request: %w", err)
	}
	repo, ok := s.fileRepo.(finalizationRepository)
	if !ok {
		return model.File{}, errors.New("durable upload finalization is not supported by the file repository")
	}
	// Persisted binding hashes include these exact JSON key names.
	//nolint:tagliatelle // Preserve durable binding hash compatibility.
	binding, err := json.Marshal(struct {
		UserUUID      string `json:"UserUUID"`
		ManagerID     int64  `json:"ManagerID"`
		UserID        int64  `json:"UserID"`
		ObjectType    string `json:"ObjectType"`
		ObjectID      int64  `json:"ObjectID"`
		ReplacementID int64  `json:"ReplacementID"`
		Name          string `json:"Name"`
		Size          int64  `json:"Size"`
	}{
		req.UploaderUUID.String(),
		req.ManagerID,
		req.UserID,
		req.ObjectType.String(),
		req.ObjectID.Int64(),
		req.DeletedID.Int64(),
		name,
		size,
	})
	if err != nil {
		return model.File{}, err
	}
	sum := sha256.Sum256(binding)
	return repo.FinalizeUpload(ctx, req.FinalizationKey, hex.EncodeToString(sum[:]), stored, create)
}

func (s *Service) validateStoredUpload(ctx context.Context, file *UploadedFile, cfg *FileUploadConfig) error {
	if strings.TrimSpace(file.OriginalName) == "" || strings.TrimSpace(file.FileName) == "" {
		return ClientError{Message: "invalid uploaded file metadata"}
	}
	key, err := filesanitize.EnsureRelativePath(file.Path)
	if err != nil {
		return fmt.Errorf("normalize stored upload path: %w", err)
	}
	if path.Base(key) != file.FileName {
		return ClientError{Message: "stored file name does not match its object key"}
	}
	file.Path = key
	file.FolderPath = filesanitize.EnsureRelativeDir(key)
	if file.Size <= 0 {
		return newUploadError(uploadErrorCodeFileEmpty, errors.New("stored upload is empty"))
	}
	if err := ensureDeclaredSize(file.Size, cfg.MaxFileSize); err != nil {
		return err
	}
	ext := strings.ToLower(filepath.Ext(file.OriginalName))
	if err := s.ensureAllowedExtension(ext, cfg.AllowedExtensions); err != nil {
		return err
	}
	if !s.isAllowedMime(ext, file.MimeType, cfg.AllowedMimeTypes) {
		return newUploadError(uploadErrorCodeMimeDenied, errors.New("stored upload MIME is denied"))
	}
	readable, ok := s.storage.(interface {
		Open(ctx context.Context, key string) (io.ReadCloser, error)
	})
	if !ok {
		return errors.New("stored uploads require readable storage for content validation")
	}
	reader, err := readable.Open(ctx, key)
	if err != nil {
		return fmt.Errorf("open stored upload: %w", err)
	}
	if reader == nil {
		return errors.New("stored upload reader is nil")
	}
	defer reader.Close()
	header := make([]byte, sniffLen)
	n, err := io.ReadFull(reader, header)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return fmt.Errorf("inspect stored upload: %w", err)
	}
	if n == 0 {
		return newUploadError(uploadErrorCodeFileEmpty, errors.New("stored upload is empty"))
	}
	mimeType := http.DetectContentType(header[:n])
	if !s.isAllowedMime(ext, mimeType, cfg.AllowedMimeTypes) {
		return newUploadError(uploadErrorCodeMimeDenied, errors.New("stored upload content is denied"))
	}
	if filepolicy.NormalizeMIME(file.MimeType) != filepolicy.NormalizeMIME(mimeType) {
		return newUploadError(uploadErrorCodeMimeDenied, errors.New("stored upload MIME does not match its content"))
	}
	if err := s.runValidatorsFromInput(ctx,
		ReaderUploadInput{
			OriginalName: file.OriginalName,
			Size:         file.Size,
		},
		ext,
		mimeType,
		header[:n]); err != nil {
		return err
	}
	file.FileType, err = model.GetFileTypeFromMimeType(mimeType)
	return err
}
