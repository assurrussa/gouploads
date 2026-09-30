package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/google/uuid"

	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

// Storage confines every filesystem operation to an opened root, including
// symlink traversal. It owns no permanent descriptors and needs no Close call.
type Storage struct{ rootDir, baseURL, publicPrefix string }

func New(rootDir, baseURL string) (*Storage, error) {
	return NewWithPublicPrefix(rootDir, baseURL, "media/v1")
}

func NewWithPublicPrefix(rootDir, baseURL, publicPrefix string) (*Storage, error) {
	if strings.TrimSpace(rootDir) == "" {
		return nil, errors.New("local storage: root dir is required")
	}
	root, err := filepath.Abs(rootDir)
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return nil, fmt.Errorf("local storage: ensure root dir: %w", err)
	}
	if publicPrefix == "" {
		publicPrefix = "media/v1"
	}
	prefix, err := sanitizeRelativePath(publicPrefix)
	if err != nil {
		return nil, fmt.Errorf("local storage: public prefix: %w", err)
	}
	return &Storage{rootDir: root, baseURL: strings.TrimRight(baseURL, "/"), publicPrefix: prefix}, nil
}

func (s *Storage) openRoot(ctx context.Context) (*os.Root, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return os.OpenRoot(s.rootDir)
}

func (s *Storage) Exists(ctx context.Context, input filestorage.ExistFileInput) (filestorage.ExistFile, error) {
	key, err := sanitizeRelativePath(input.Path)
	if err != nil {
		return filestorage.ExistFile{}, fmt.Errorf("local storage: path: %w", err)
	}
	root, err := s.openRoot(ctx)
	if err != nil {
		return filestorage.ExistFile{}, err
	}
	defer root.Close()
	info, err := root.Stat(key)
	if errors.Is(err, os.ErrNotExist) {
		return filestorage.ExistFile{}, nil
	}
	if err != nil {
		return filestorage.ExistFile{}, fmt.Errorf("local storage: stat file: %w", err)
	}
	return filestorage.ExistFile{Exist: info.Mode().IsRegular()}, nil
}

func (s *Storage) SavePersist(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
	directory, err := sanitizeRelativePath(input.Dir)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: dir: %w", err)
	}
	if directory == s.publicPrefix || strings.HasPrefix(directory, s.publicPrefix+"/") {
		input.Dir = directory
	} else {
		input.Dir = filestorage.DirPersistPath(directory)
	}
	return s.saveFile(ctx, input, true)
}

func (s *Storage) SaveTemp(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
	directory, err := sanitizeRelativePath(input.Dir)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: dir: %w", err)
	}
	input.Dir = filestorage.DirTemptPath(directory)
	return s.saveFile(ctx, input, false)
}

func (s *Storage) Commit(ctx context.Context, input filestorage.CommitInput) (filestorage.StoredFile, error) {
	if err := input.Validate(); err != nil {
		return filestorage.StoredFile{}, err
	}
	source, err := sanitizeRelativePath(input.Path)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	directory, err := sanitizeRelativePath(input.DestDir)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	name, err := sanitizeFileName(input.FileName)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	destination := path.Join(directory, name)
	root, err := s.openRoot(ctx)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	defer root.Close()
	file, err := root.Open(source)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: open source: %w", err)
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	if !info.Mode().IsRegular() {
		return filestorage.StoredFile{}, errors.New("local storage: source is not a regular file")
	}
	if source == destination {
		return filestorage.StoredFile{RelativePath: destination, URL: s.buildURL(destination), Size: info.Size()}, nil
	}
	if err := root.MkdirAll(directory, 0o755); err != nil {
		return filestorage.StoredFile{}, err
	}
	written, err := writeRootFile(ctx, root, directory, destination, file, true)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	// Close before unlinking for Windows. Never destroy staging until the new
	// artifact has been written, synced and atomically renamed successfully.
	if err := file.Close(); err != nil {
		return filestorage.StoredFile{}, err
	}
	if err := root.Remove(source); err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: remove source: %w", err)
	}
	cleanupRootParents(root, source)
	return filestorage.StoredFile{RelativePath: destination, URL: s.buildURL(destination), Size: written}, nil
}

func (s *Storage) Open(ctx context.Context, relativePath string) (io.ReadCloser, error) {
	key, err := sanitizeRelativePath(relativePath)
	if err != nil {
		return nil, fmt.Errorf("local storage: open path: %w", err)
	}
	root, err := s.openRoot(ctx)
	if err != nil {
		return nil, err
	}
	defer root.Close()
	file, err := root.Open(key)
	if err != nil {
		return nil, fmt.Errorf("local storage: open file: %w", err)
	}
	info, err := file.Stat()
	if err != nil {
		_ = file.Close()
		return nil, err
	}
	if !info.Mode().IsRegular() {
		_ = file.Close()
		return nil, errors.New("local storage: not a regular file")
	}
	return &contextReadCloser{ctx: ctx, File: file}, nil
}

func (s *Storage) Delete(ctx context.Context, key string) error {
	return s.DeleteBatch(ctx, []string{key})
}

func (s *Storage) DeleteBatch(ctx context.Context, relativePaths []string) error {
	keys := make([]string, 0, len(relativePaths))
	seen := make(map[string]bool, len(relativePaths))
	// Validate the whole plan before deleting any member of the plan.
	for _, raw := range relativePaths {
		key, err := sanitizeRelativePath(raw)
		if err != nil {
			return fmt.Errorf("local storage: delete path: %w", err)
		}
		if !seen[key] {
			seen[key] = true
			keys = append(keys, key)
		}
	}
	if len(keys) == 0 {
		return nil
	}
	root, err := s.openRoot(ctx)
	if err != nil {
		return err
	}
	defer root.Close()
	for _, key := range keys {
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := root.Remove(key); err != nil && !errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("local storage: delete file: %w", err)
		}
		cleanupRootParents(root, key)
	}
	return nil
}

func (s *Storage) saveFile(ctx context.Context, input filestorage.SaveFileInput, replace bool) (filestorage.StoredFile, error) {
	if err := input.Validate(); err != nil {
		return filestorage.StoredFile{}, err
	}
	directory, err := sanitizeRelativePath(input.Dir)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	name, err := sanitizeFileName(input.FileName)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: file name: %w", err)
	}
	key := path.Join(directory, name)
	root, err := s.openRoot(ctx)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	defer root.Close()
	if err := root.MkdirAll(directory, 0o755); err != nil {
		return filestorage.StoredFile{}, err
	}
	size, err := writeRootFile(ctx, root, directory, key, input.Reader, replace)
	if err != nil {
		return filestorage.StoredFile{}, err
	}
	return filestorage.StoredFile{RelativePath: key, URL: s.buildURL(key), Size: size, MimeType: input.MimeType}, nil
}

func writeRootFile(
	ctx context.Context, root *os.Root, directory, destination string, reader io.Reader, replace bool,
) (int64, error) {
	target := destination
	if replace {
		target = path.Join(directory, ".gouploads-persist-"+uuid.NewString())
	}
	mode := os.FileMode(0o600)
	if replace {
		mode = 0o644
	}
	file, err := root.OpenFile(target, os.O_CREATE|os.O_EXCL|os.O_WRONLY, mode)
	if err != nil {
		return 0, fmt.Errorf("local storage: create file: %w", err)
	}
	committed := false
	defer func() {
		_ = file.Close()
		if !committed {
			_ = root.Remove(target)
		}
	}()
	size, err := io.Copy(file, &contextReader{ctx: ctx, reader: reader})
	if err != nil {
		return 0, fmt.Errorf("local storage: write file: %w", err)
	}
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	if err := file.Sync(); err != nil {
		return 0, err
	}
	if err := file.Close(); err != nil {
		return 0, err
	}
	if replace {
		if err := root.Rename(target, destination); err != nil {
			return 0, fmt.Errorf("local storage: replace file: %w", err)
		}
	}
	committed = true
	if runtime.GOOS != "windows" {
		dir, err := root.Open(directory)
		if err != nil {
			return 0, err
		}
		syncErr := dir.Sync()
		closeErr := dir.Close()
		if syncErr != nil {
			return 0, syncErr
		}
		if closeErr != nil {
			return 0, closeErr
		}
	}
	return size, nil
}

//nolint:containedctx // Reader must check its operation context on every Read.
type contextReader struct {
	ctx    context.Context
	reader io.Reader
}

func (r *contextReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.reader.Read(p)
}

//nolint:containedctx // Stream lifetime uses its operation context for cancellation.
type contextReadCloser struct {
	ctx context.Context
	*os.File
}

func (r *contextReadCloser) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}
	return r.File.Read(p)
}

// Prevent os.File.WriteTo from bypassing the context-aware Read method.
func (r *contextReadCloser) WriteTo(w io.Writer) (int64, error) {
	return io.Copy(w, &contextReader{ctx: r.ctx, reader: r.File})
}

func cleanupRootParents(root *os.Root, key string) {
	for directory := path.Dir(key); directory != "." && directory != "" && directory != "/"; directory = path.Dir(directory) {
		if err := root.Remove(directory); err != nil && !errors.Is(err, os.ErrNotExist) {
			return
		}
	}
}

func (s *Storage) buildURL(key string) string {
	if s.baseURL == "" || s.baseURL == "/" {
		return "/" + strings.TrimLeft(key, "/")
	}
	return s.baseURL + "/" + strings.TrimLeft(key, "/")
}

func sanitizeRelativePath(raw string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(raw, "\\", "/"))
	if value == "" {
		return "", errors.New("empty path")
	}
	if strings.HasPrefix(value, "/") || strings.Contains(value, ":") || strings.ContainsRune(value, 0) {
		return "", errors.New("path must be relative")
	}
	clean := path.Clean(value)
	if clean == "." || clean == "" {
		return "", errors.New("path resolves to root")
	}
	if clean == ".." || strings.HasPrefix(clean, "../") {
		return "", errors.New("path escapes root")
	}
	return clean, nil
}

func sanitizeFileName(raw string) (string, error) {
	name := strings.TrimSpace(raw)
	if name == "" {
		return "", errors.New("empty file name")
	}
	if strings.ContainsAny(name, "/\\:\x00") {
		return "", errors.New("file name contains path separators")
	}
	if name == "." || name == ".." {
		return "", errors.New("invalid file name")
	}
	return name, nil
}
func toSlash(value string) string { return strings.TrimLeft(filepath.ToSlash(value), "/") }
func isDirEmpty(directory string) (bool, error) {
	entries, err := os.ReadDir(directory)
	if errors.Is(err, os.ErrNotExist) {
		return true, nil
	}
	return len(entries) == 0, err
}
