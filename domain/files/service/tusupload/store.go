package tusupload

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"time"

	"github.com/google/uuid"

	"github.com/assurrussa/gouploads/internal/filelock"
	sharedtypes "github.com/assurrussa/gouploads/internal/identity"
)

var (
	ErrNotFound        = errors.New("tus upload not found")
	ErrOffsetMismatch  = errors.New("tus upload offset mismatch")
	ErrLengthExceeded  = errors.New("tus upload length exceeded")
	ErrChunkTooSmall   = errors.New("tus upload chunk is too small")
	ErrChunkSize       = errors.New("tus upload intermediate chunk must match the configured part size")
	ErrUploadBusy      = errors.New("tus upload is busy")
	ErrFenceLost       = errors.New("tus upload fence lost")
	ErrUploadFinalized = errors.New("tus upload is already finalized")
)

type Status string

const (
	StatusActive     Status = "active"
	StatusFinalizing Status = "finalizing"
	StatusReady      Status = "ready"
	StatusCleaning   Status = "cleaning"
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
	ID              string             `json:"id"`
	UploadLength    int64              `json:"uploadLength"`
	Offset          int64              `json:"offset"`
	Metadata        map[string]string  `json:"metadata,omitempty"`
	Path            string             `json:"path"`
	URL             string             `json:"url,omitempty"`
	OriginalName    string             `json:"originalName"`
	FileName        string             `json:"fileName"`
	MimeType        string             `json:"mimeType,omitempty"`
	OwnerID         int64              `json:"ownerId"`
	OwnerUUID       sharedtypes.UserID `json:"ownerUuid"`
	CreatedAt       time.Time          `json:"createdAt"`
	UpdatedAt       time.Time          `json:"updatedAt"`
	Status          Status             `json:"status"`
	Revision        int64              `json:"revision"`
	Quarantined     bool               `json:"quarantined"`
	FinalizationKey string             `json:"finalizationKey,omitempty"`
}

type CompleteResult struct {
	Reader          io.ReadCloser
	Size            int64
	RelativePath    string
	URL             string
	MimeType        string
	OriginalName    string
	FileName        string
	Width           int
	Height          int
	FinalizationKey string
	Quarantined     bool
}

// FileStore uses a local filesystem, not a distributed filesystem profile.
// Locks coordinate independent instances/processes sharing that local root.
// A bounded set of lock files is permanent: unlinking live locks is unsafe.
type FileStore struct{ root string }

func NewFileStore(root string) (*FileStore, error) {
	if root == "" {
		return nil, errors.New("tus store root is required")
	}
	absolute, err := filepath.Abs(root)
	if err != nil {
		return nil, fmt.Errorf("resolve tus root: %w", err)
	}
	if err := os.MkdirAll(absolute, 0o700); err != nil {
		return nil, fmt.Errorf("create tus store root: %w", err)
	}
	return &FileStore{root: absolute}, nil
}

func validSessionID(id string) bool {
	parsed, err := uuid.Parse(id)
	return err == nil && parsed != uuid.Nil && parsed.String() == id
}

func (s *FileStore) withSession(ctx context.Context, id string, fn func(*os.Root) error) error {
	if !validSessionID(id) {
		return fmt.Errorf("%w: invalid session id", ErrNotFound)
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return fmt.Errorf("open tus root: %w", err)
	}
	defer root.Close()
	if err := root.MkdirAll(".locks", 0o700); err != nil {
		return err
	}
	hash := fnv.New32a()
	_, _ = hash.Write([]byte(id))
	lockPath := fmt.Sprintf(".locks/%02x.lock", hash.Sum32()%128)
	lock, err := root.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return fmt.Errorf("open tus lock: %w", err)
	}
	defer lock.Close()
	release, err := filelock.Acquire(ctx, lock)
	if err != nil {
		return fmt.Errorf("lock tus session: %w", err)
	}
	defer func() { _ = release() }()
	return fn(root)
}

func (s *FileStore) Create(ctx context.Context, req CreateRequest) (Session, error) {
	if req.UploadLength < 0 {
		return Session{}, fmt.Errorf("invalid upload length: %d", req.UploadLength)
	}
	if req.OriginalName == "" || req.FileName == "" {
		return Session{}, errors.New("original name and file name are required")
	}
	id := uuid.NewString()
	var result Session
	err := s.withSession(ctx, id, func(root *os.Root) error {
		if err := root.Mkdir(id, 0o700); err != nil {
			return fmt.Errorf("create tus upload dir: %w", err)
		}
		file, err := root.OpenFile(path.Join(id, "data"), os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
		if err != nil {
			return fmt.Errorf("create tus data file: %w", err)
		}
		if err := file.Close(); err != nil {
			return err
		}
		now := time.Now().UTC()
		meta := storeMeta{
			ID: id, UploadLength: req.UploadLength, OriginalName: req.OriginalName, FileName: req.FileName,
			Metadata: cloneMetadata(req.Metadata), OwnerID: req.OwnerID, OwnerUUID: req.OwnerUUID,
			CreatedAt: now, UpdatedAt: now, Status: StatusActive,
		}
		if err := writeMetaAt(root, id, meta); err != nil {
			return err
		}
		if err := syncDirectory(root, "."); err != nil {
			return err
		}
		result = s.session(meta, 0)
		return nil
	})
	return result, err
}

func (s *FileStore) session(meta storeMeta, size int64) Session {
	status := meta.Status
	if status == "" {
		status = StatusActive
	}
	return Session{
		ID: meta.ID, UploadLength: meta.UploadLength, Offset: size,
		Metadata: cloneMetadata(meta.Metadata), Path: filepath.Join(s.root, meta.ID, "data"),
		OriginalName: meta.OriginalName, FileName: meta.FileName, MimeType: meta.MimeType,
		OwnerID: meta.OwnerID, OwnerUUID: meta.OwnerUUID, CreatedAt: meta.CreatedAt, UpdatedAt: meta.UpdatedAt,
		Status: status, Quarantined: true, FinalizationKey: meta.ID,
	}
}

func (s *FileStore) load(root *os.Root, id string) (Session, storeMeta, error) {
	meta, err := readMetaAt(root, id)
	if err != nil {
		return Session{}, storeMeta{}, err
	}
	if meta.ID != id || meta.UploadLength < 0 {
		return Session{}, storeMeta{}, errors.New("invalid tus session metadata")
	}
	info, err := root.Stat(path.Join(id, "data"))
	if errors.Is(err, os.ErrNotExist) {
		return Session{}, storeMeta{}, ErrNotFound
	}
	if err != nil {
		return Session{}, storeMeta{}, fmt.Errorf("stat tus data: %w", err)
	}
	if !info.Mode().IsRegular() || info.Size() > meta.UploadLength {
		return Session{}, storeMeta{}, errors.New("invalid tus data file size or mode")
	}
	return s.session(meta, info.Size()), meta, nil
}

func (s *FileStore) Get(ctx context.Context, id string) (Session, error) {
	var result Session
	err := s.withSession(ctx, id, func(root *os.Root) error {
		var err error
		result, _, err = s.load(root, id)
		return err
	})
	return result, err
}

// ReadPrefix returns a bounded stored prefix at an exact protocol offset.
// It shares the session lock with Append and never exposes an unconfined path.
func (s *FileStore) ReadPrefix(ctx context.Context, id string, offset int64) ([]byte, error) {
	var prefix []byte
	err := s.withSession(ctx, id, func(root *os.Root) error {
		session, _, err := s.load(root, id)
		if err != nil {
			return err
		}
		if offset < 0 || session.Offset != offset {
			return ErrOffsetMismatch
		}
		file, err := root.Open(path.Join(id, "data"))
		if err != nil {
			return fmt.Errorf("open tus prefix: %w", err)
		}
		defer file.Close()
		prefix = make([]byte, min(offset, int64(SniffLen)))
		_, err = io.ReadFull(file, prefix)
		return err
	})
	return prefix, err
}

func (s *FileStore) Append(ctx context.Context, id string, offset int64, chunk []byte, mimeType string) (int64, error) {
	var newOffset int64
	err := s.withSession(ctx, id, func(root *os.Root) error {
		session, meta, err := s.load(root, id)
		if err != nil {
			return err
		}
		newOffset = session.Offset
		if offset < 0 || offset != session.Offset {
			return ErrOffsetMismatch
		}
		if session.Status != StatusActive {
			return ErrUploadFinalized
		}
		// Subtraction avoids overflow and checks before touching the data file.
		if int64(len(chunk)) > session.UploadLength-offset {
			return ErrLengthExceeded
		}
		if len(chunk) == 0 {
			return nil
		}
		file, err := root.OpenFile(path.Join(id, "data"), os.O_WRONLY, 0o600)
		if err != nil {
			return fmt.Errorf("open tus data: %w", err)
		}
		written, writeErr := file.WriteAt(chunk, offset)
		if writeErr == nil && written != len(chunk) {
			writeErr = io.ErrShortWrite
		}
		if writeErr == nil {
			writeErr = file.Sync()
		}
		if writeErr != nil {
			// This operation owns the tail. Do not leave a rejected partial PATCH.
			truncateErr := file.Truncate(offset)
			syncErr := file.Sync()
			closeErr := file.Close()
			return errors.Join(writeErr, truncateErr, syncErr, closeErr)
		}
		if err := file.Close(); err != nil {
			return err
		}
		newOffset = offset + int64(written)
		if meta.MimeType == "" && mimeType != "" {
			meta.MimeType = mimeType
		}
		meta.UpdatedAt = time.Now().UTC()
		return writeMetaAt(root, id, meta)
	})
	return newOffset, err
}

func (s *FileStore) Complete(ctx context.Context, id string) (CompleteResult, error) {
	var result CompleteResult
	err := s.withSession(ctx, id, func(root *os.Root) error {
		session, meta, err := s.load(root, id)
		if err != nil {
			return err
		}
		if session.Offset != session.UploadLength {
			return ErrOffsetMismatch
		}
		meta.Status = StatusReady
		meta.UpdatedAt = time.Now().UTC()
		if err := writeMetaAt(root, id, meta); err != nil {
			return err
		}
		file, err := root.Open(path.Join(id, "data"))
		if err != nil {
			return fmt.Errorf("open completed tus data: %w", err)
		}
		result = CompleteResult{
			Reader: file, Size: session.Offset, OriginalName: session.OriginalName, FileName: session.FileName,
			MimeType: session.MimeType, FinalizationKey: session.FinalizationKey, Quarantined: true,
		}
		return nil
	})
	return result, err
}

func (s *FileStore) Delete(ctx context.Context, id string) error {
	return s.withSession(ctx, id, func(root *os.Root) error {
		if _, err := root.Lstat(id); err != nil {
			if errors.Is(err, os.ErrNotExist) {
				return ErrNotFound
			}
			return err
		}
		return root.RemoveAll(id)
	})
}

func (s *FileStore) Cleanup(ctx context.Context, before time.Time) (int, error) {
	if before.IsZero() {
		return 0, errors.New("cleanup threshold is required")
	}
	root, err := os.OpenRoot(s.root)
	if err != nil {
		return 0, err
	}
	defer root.Close()
	directory, err := root.Open(".")
	if err != nil {
		return 0, err
	}
	defer directory.Close()
	removed := 0
	for {
		entries, readErr := directory.ReadDir(100)
		if readErr != nil && !errors.Is(readErr, io.EOF) {
			return removed, readErr
		}
		for _, entry := range entries {
			if err := ctx.Err(); err != nil {
				return removed, err
			}
			// Do not touch .locks, unrelated directories, or symlinks.
			if !entry.IsDir() || !validSessionID(entry.Name()) {
				continue
			}
			deleted, err := s.cleanupSession(ctx, entry.Name(), before)
			if deleted {
				removed++
			}
			if err != nil {
				return removed, err
			}
		}
		if errors.Is(readErr, io.EOF) {
			return removed, nil
		}
	}
}

func (s *FileStore) cleanupSession(ctx context.Context, id string, before time.Time) (bool, error) {
	removed := false
	err := s.withSession(ctx, id, func(root *os.Root) error {
		cutoff, found, err := cleanupSessionActivity(root, id)
		if err != nil || !found || !cutoff.Before(before) {
			return err
		}
		// Activity is re-read under the same lock used by PATCH/complete.
		if err := root.RemoveAll(id); err != nil {
			return err
		}
		removed = true
		return nil
	})
	return removed, err
}

func cleanupSessionActivity(root *os.Root, id string) (time.Time, bool, error) {
	info, err := root.Lstat(id)
	if errors.Is(err, os.ErrNotExist) {
		return time.Time{}, false, nil
	}
	if err != nil {
		return time.Time{}, false, err
	}
	if !info.IsDir() {
		return time.Time{}, false, nil
	}
	cutoff := info.ModTime()
	meta, err := readMetaAt(root, id)
	if err != nil && !errors.Is(err, ErrNotFound) {
		return time.Time{}, false, err
	}
	if err == nil {
		if meta.ID != id {
			return time.Time{}, false, errors.New("invalid tus cleanup metadata")
		}
		if !meta.UpdatedAt.IsZero() {
			cutoff = meta.UpdatedAt
		}
	}
	return cutoff, true, nil
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
	Status       Status             `json:"status,omitempty"`
}

func readMetaAt(root *os.Root, id string) (storeMeta, error) {
	data, err := root.ReadFile(path.Join(id, "meta.json"))
	if errors.Is(err, os.ErrNotExist) {
		return storeMeta{}, ErrNotFound
	}
	if err != nil {
		return storeMeta{}, fmt.Errorf("read tus metadata: %w", err)
	}
	var meta storeMeta
	if err := json.Unmarshal(data, &meta); err != nil {
		return storeMeta{}, fmt.Errorf("decode tus metadata: %w", err)
	}
	return meta, nil
}

func writeMetaAt(root *os.Root, id string, meta storeMeta) error {
	data, err := json.Marshal(meta)
	if err != nil {
		return err
	}
	temporary := path.Join(id, ".meta-"+uuid.NewString()+".tmp")
	file, err := root.OpenFile(temporary, os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0o600)
	if err != nil {
		return err
	}
	defer func() { _ = root.Remove(temporary) }()
	_, writeErr := file.Write(data)
	if writeErr == nil {
		writeErr = file.Sync()
	}
	if err := errors.Join(writeErr, file.Close()); err != nil {
		return err
	}
	if err := root.Rename(temporary, path.Join(id, "meta.json")); err != nil {
		return err
	}
	return syncDirectory(root, id)
}

func syncDirectory(root *os.Root, name string) error {
	// Windows does not expose directory fsync through os.File.Sync.
	if runtime.GOOS == "windows" {
		return nil
	}
	dir, err := root.Open(name)
	if err != nil {
		return err
	}
	defer dir.Close()
	return dir.Sync()
}

//nolint:tagliatelle // read the old on-disk snake_case format
func (m *storeMeta) UnmarshalJSON(data []byte) error {
	type alias storeMeta
	var current alias
	if err := json.Unmarshal(data, &current); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(data, &fields); err != nil {
		return err
	}
	// The shared `id` field cannot be used to distinguish legacy metadata.
	if _, legacy := fields["upload_length"]; legacy {
		if _, modern := fields["uploadLength"]; !modern {
			var old struct {
				UploadLength int64              `json:"upload_length"`
				OriginalName string             `json:"original_name"`
				FileName     string             `json:"file_name"`
				MimeType     string             `json:"mime_type"`
				OwnerID      int64              `json:"owner_id"`
				OwnerUUID    sharedtypes.UserID `json:"owner_uuid"`
				CreatedAt    time.Time          `json:"created_at"`
				UpdatedAt    time.Time          `json:"updated_at"`
			}
			if err := json.Unmarshal(data, &old); err != nil {
				return err
			}
			current.UploadLength = old.UploadLength
			current.OriginalName = old.OriginalName
			current.FileName = old.FileName
			current.MimeType = old.MimeType
			current.OwnerID = old.OwnerID
			current.OwnerUUID = old.OwnerUUID
			current.CreatedAt = old.CreatedAt
			current.UpdatedAt = old.UpdatedAt
		}
	}
	*m = storeMeta(current)
	return nil
}

func cloneMetadata(meta map[string]string) map[string]string {
	if len(meta) == 0 {
		return nil
	}
	cloned := make(map[string]string, len(meta))
	for key, value := range meta {
		cloned[key] = value
	}
	return cloned
}
