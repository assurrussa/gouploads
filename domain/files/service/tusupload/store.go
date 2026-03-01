package tusupload

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"time"

	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/google/uuid"
)

var (
	ErrNotFound       = errors.New("tus upload not found")
	ErrOffsetMismatch = errors.New("tus upload offset mismatch")
	ErrLengthExceeded = errors.New("tus upload length exceeded")
	ErrChunkTooSmall  = errors.New("tus upload chunk is too small")
)

type Store interface {
	Create(ctx context.Context, req CreateRequest) (Session, error)
	Get(ctx context.Context, id string) (Session, error)
	Append(ctx context.Context, id string, offset int64, chunk []byte, mimeType string) (int64, error)
	Complete(ctx context.Context, id string) (CompleteResult, error)
	Delete(ctx context.Context, id string) error
	Cleaner
}

type Cleaner interface {
	Cleanup(ctx context.Context, before time.Time) (int, error)
}

type CreateRequest struct {
	UploadLength int64
	OriginalName string
	FileName     string
	Metadata     map[string]string
	OwnerID      int64
	OwnerUUID    sharedtypes.UserID
}

type Session struct {
	ID           string             `json:"id"`
	UploadLength int64              `json:"uploadLength"`
	Offset       int64              `json:"offset"`
	Metadata     map[string]string  `json:"metadata,omitempty"`
	Path         string             `json:"path"`
	URL          string             `json:"url,omitempty"`
	OriginalName string             `json:"originalName"`
	FileName     string             `json:"fileName"`
	MimeType     string             `json:"mimeType,omitempty"`
	OwnerID      int64              `json:"ownerId"`
	OwnerUUID    sharedtypes.UserID `json:"ownerUuid"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
}

type CompleteResult struct {
	Reader       io.ReadCloser
	Size         int64
	RelativePath string
	URL          string
	MimeType     string
	OriginalName string
	FileName     string
	Width        int
	Height       int
}

type FileStore struct {
	root string
}

func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, errors.New("tus store root is required")
	}

	clean := filepath.Clean(root)
	if err := os.MkdirAll(clean, 0o755); err != nil {
		return nil, fmt.Errorf("create tus store root: %w", err)
	}

	return &FileStore{root: clean}, nil
}

func (s *FileStore) Create(_ context.Context, req CreateRequest) (Session, error) {
	if req.UploadLength < 0 {
		return Session{}, fmt.Errorf("invalid upload length: %d", req.UploadLength)
	}
	if req.OriginalName == "" {
		return Session{}, errors.New("original name is required")
	}
	if req.FileName == "" {
		return Session{}, errors.New("file name is required")
	}

	id := uuid.NewString()
	dir := filepath.Join(s.root, id)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Session{}, fmt.Errorf("create tus upload dir: %w", err)
	}

	dataPath := filepath.Join(dir, "data")
	file, err := os.OpenFile(dataPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
	if err != nil {
		return Session{}, fmt.Errorf("create tus data file: %w", err)
	}
	if err := file.Close(); err != nil {
		return Session{}, fmt.Errorf("close tus data file: %w", err)
	}

	now := time.Now()
	meta := storeMeta{
		ID:           id,
		UploadLength: req.UploadLength,
		OriginalName: req.OriginalName,
		FileName:     req.FileName,
		Metadata:     cloneMetadata(req.Metadata),
		OwnerID:      req.OwnerID,
		OwnerUUID:    req.OwnerUUID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}

	if err := writeMeta(filepath.Join(dir, "meta.json"), meta); err != nil {
		return Session{}, err
	}

	return Session{
		ID:           id,
		UploadLength: req.UploadLength,
		Offset:       0,
		Metadata:     cloneMetadata(req.Metadata),
		Path:         dataPath,
		OriginalName: req.OriginalName,
		FileName:     req.FileName,
		OwnerID:      req.OwnerID,
		OwnerUUID:    req.OwnerUUID,
		CreatedAt:    now,
		UpdatedAt:    now,
	}, nil
}

func (s *FileStore) Get(_ context.Context, id string) (Session, error) {
	dir := filepath.Join(s.root, id)
	meta, err := readMeta(filepath.Join(dir, "meta.json"))
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return Session{}, ErrNotFound
		}
		return Session{}, err
	}

	dataPath := filepath.Join(dir, "data")
	info, err := os.Stat(dataPath)
	if err != nil {
		if os.IsNotExist(err) {
			return Session{}, ErrNotFound
		}
		return Session{}, fmt.Errorf("stat tus data file: %w", err)
	}

	return Session{
		ID:           meta.ID,
		UploadLength: meta.UploadLength,
		Offset:       info.Size(),
		Metadata:     cloneMetadata(meta.Metadata),
		Path:         dataPath,
		OriginalName: meta.OriginalName,
		FileName:     meta.FileName,
		MimeType:     meta.MimeType,
		OwnerID:      meta.OwnerID,
		OwnerUUID:    meta.OwnerUUID,
		CreatedAt:    meta.CreatedAt,
		UpdatedAt:    meta.UpdatedAt,
	}, nil
}

func (s *FileStore) Append(ctx context.Context, id string, offset int64, chunk []byte, mimeType string) (int64, error) {
	session, err := s.Get(ctx, id)
	if err != nil {
		return 0, err
	}

	if offset != session.Offset {
		return session.Offset, ErrOffsetMismatch
	}

	if session.UploadLength >= 0 && offset > session.UploadLength {
		return session.Offset, ErrLengthExceeded
	}

	file, err := os.OpenFile(session.Path, os.O_WRONLY, 0o644)
	if err != nil {
		if os.IsNotExist(err) {
			return 0, ErrNotFound
		}
		return 0, fmt.Errorf("open tus data file: %w", err)
	}
	defer file.Close()

	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		return 0, fmt.Errorf("seek tus data file: %w", err)
	}

	written, err := io.Copy(file, bytes.NewReader(chunk))
	if err != nil {
		return offset, fmt.Errorf("write tus data file: %w", err)
	}

	newOffset := offset + written
	if session.UploadLength >= 0 && newOffset > session.UploadLength {
		return offset, ErrLengthExceeded
	}

	if mimeType != "" && session.MimeType == "" && offset == 0 {
		if err := updateMetaMimeType(filepath.Join(s.root, id, "meta.json"), mimeType); err != nil {
			return newOffset, err
		}
	}

	if err := updateMetaUpdatedAt(filepath.Join(s.root, id, "meta.json"), time.Now()); err != nil {
		return newOffset, err
	}

	return newOffset, nil
}

func (s *FileStore) Complete(ctx context.Context, id string) (CompleteResult, error) {
	session, err := s.Get(ctx, id)
	if err != nil {
		return CompleteResult{}, err
	}

	if session.UploadLength >= 0 && session.Offset < session.UploadLength {
		return CompleteResult{}, ErrOffsetMismatch
	}

	file, err := os.Open(session.Path)
	if err != nil {
		if os.IsNotExist(err) {
			return CompleteResult{}, ErrNotFound
		}
		return CompleteResult{}, fmt.Errorf("open tus data file: %w", err)
	}

	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return CompleteResult{}, fmt.Errorf("stat tus data file: %w", err)
	}

	return CompleteResult{
		Reader:       file,
		Size:         info.Size(),
		OriginalName: session.OriginalName,
		FileName:     session.FileName,
		MimeType:     session.MimeType,
	}, nil
}

func (s *FileStore) Delete(_ context.Context, id string) error {
	dir := filepath.Join(s.root, id)
	if err := os.RemoveAll(dir); err != nil {
		if os.IsNotExist(err) {
			return ErrNotFound
		}
		return fmt.Errorf("delete tus upload dir: %w", err)
	}

	return nil
}

func (s *FileStore) Cleanup(ctx context.Context, before time.Time) (int, error) {
	entries, err := os.ReadDir(s.root)
	if err != nil {
		return 0, fmt.Errorf("read tus store root: %w", err)
	}

	removed := 0
	for _, entry := range entries {
		if err := ctx.Err(); err != nil {
			return removed, err
		}
		if !entry.IsDir() {
			continue
		}
		count, err := s.cleanupEntry(entry, before)
		if err != nil {
			return removed, err
		}
		removed += count
	}

	return removed, nil
}

func (s *FileStore) cleanupEntry(entry os.DirEntry, before time.Time) (int, error) {
	dirPath := filepath.Join(s.root, entry.Name())
	metaPath := filepath.Join(dirPath, "meta.json")

	meta, err := readMeta(metaPath)
	if err != nil {
		if errors.Is(err, ErrNotFound) {
			return s.cleanupByModTime(entry, dirPath, before)
		}
		return 0, err
	}

	cutoff, err := entryModTime(entry, meta.UpdatedAt)
	if err != nil {
		return 0, err
	}

	if cutoff.Before(before) {
		if err := os.RemoveAll(dirPath); err != nil && !os.IsNotExist(err) {
			return 0, fmt.Errorf("delete tus dir: %w", err)
		}
		return 1, nil
	}

	return 0, nil
}

func (s *FileStore) cleanupByModTime(entry os.DirEntry, dirPath string, before time.Time) (int, error) {
	cutoff, err := entryModTime(entry, time.Time{})
	if err != nil {
		return 0, err
	}

	if cutoff.Before(before) {
		if err := os.RemoveAll(dirPath); err != nil && !os.IsNotExist(err) {
			return 0, fmt.Errorf("delete tus dir: %w", err)
		}
		return 1, nil
	}

	return 0, nil
}

func entryModTime(entry os.DirEntry, fallback time.Time) (time.Time, error) {
	if !fallback.IsZero() {
		return fallback, nil
	}
	info, err := entry.Info()
	if err != nil {
		return time.Time{}, fmt.Errorf("stat tus dir: %w", err)
	}
	return info.ModTime(), nil
}

type storeMeta struct {
	ID           string             `json:"id"`
	UploadLength int64              `json:"uploadLength"`
	OriginalName string             `json:"originalName"`
	FileName     string             `json:"fileName"`
	MimeType     string             `json:"mimeType"`
	Metadata     map[string]string  `json:"metadata,omitempty"`
	OwnerID      int64              `json:"ownerId"`
	OwnerUUID    sharedtypes.UserID `json:"ownerUuid"`
	CreatedAt    time.Time          `json:"createdAt"`
	UpdatedAt    time.Time          `json:"updatedAt"`
}

func writeMeta(path string, meta storeMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return fmt.Errorf("marshal tus meta: %w", err)
	}

	if err := os.WriteFile(path, data, 0o600); err != nil {
		return fmt.Errorf("write tus meta: %w", err)
	}

	return nil
}

func readMeta(path string) (storeMeta, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return storeMeta{}, ErrNotFound
		}
		return storeMeta{}, fmt.Errorf("read tus meta: %w", err)
	}

	var meta storeMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return storeMeta{}, fmt.Errorf("unmarshal tus meta: %w", err)
	}

	return meta, nil
}

//nolint:tagliatelle // keep legacy snake_case tags so old metadata files can still be read
type storeMetaLegacy struct {
	ID           string             `json:"id"`
	UploadLength int64              `json:"upload_length"`
	OriginalName string             `json:"original_name"`
	FileName     string             `json:"file_name"`
	MimeType     string             `json:"mime_type"`
	Metadata     map[string]string  `json:"metadata"`
	OwnerID      int64              `json:"owner_id"`
	OwnerUUID    sharedtypes.UserID `json:"owner_uuid"`
	CreatedAt    time.Time          `json:"created_at"`
	UpdatedAt    time.Time          `json:"updated_at"`
}

func (m *storeMeta) UnmarshalJSON(data []byte) error {
	type alias storeMeta
	var current alias
	if err := json.Unmarshal(data, &current); err == nil {
		if current.ID != "" || current.UploadLength != 0 || len(current.Metadata) > 0 {
			*m = storeMeta(current)
			return nil
		}
	}

	var legacy storeMetaLegacy
	if err := json.Unmarshal(data, &legacy); err != nil {
		return err
	}

	*m = storeMeta(legacy)
	return nil
}

func updateMetaUpdatedAt(path string, updatedAt time.Time) error {
	meta, err := readMeta(path)
	if err != nil {
		return err
	}

	meta.UpdatedAt = updatedAt
	return writeMeta(path, meta)
}

func updateMetaMimeType(path string, mimeType string) error {
	meta, err := readMeta(path)
	if err != nil {
		return err
	}

	meta.MimeType = mimeType
	return writeMeta(path, meta)
}

func cloneMetadata(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return nil
	}

	cloned := make(map[string]string, len(meta))
	for k, v := range meta {
		cloned[k] = v
	}
	return cloned
}
