package local

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"

	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

// Storage реализует сохранение файлов в локальной файловой системе.
type Storage struct {
	rootDir string
	baseURL string
}

// New создаёт адаптер локального хранилища.
func New(rootDir, baseURL string) (*Storage, error) {
	if rootDir == "" {
		return nil, errors.New("local storage: root dir is required")
	}

	cleanRoot := filepath.Clean(rootDir)
	if err := os.MkdirAll(cleanRoot, 0o755); err != nil {
		return nil, fmt.Errorf("local storage: ensure root dir: %w", err)
	}

	return &Storage{
		rootDir: cleanRoot,
		baseURL: strings.TrimRight(baseURL, "/"),
	}, nil
}

func (s *Storage) Exists(_ context.Context, input filestorage.ExistFileInput) (filestorage.ExistFile, error) {
	relPath, err := sanitizeRelativePath(input.Path)
	if err != nil {
		return filestorage.ExistFile{}, fmt.Errorf("local storage: path: %w", err)
	}

	absPath := filepath.Join(s.rootDir, relPath)

	_, err = os.Stat(absPath)
	if err != nil {
		if os.IsNotExist(err) {
			return filestorage.ExistFile{Exist: false}, nil
		}
		return filestorage.ExistFile{}, fmt.Errorf("local storage: stat file: %w", err)
	}

	return filestorage.ExistFile{Exist: true}, nil
}

func (s *Storage) SavePersist(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
	relDir, err := sanitizeRelativePath(input.Dir)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: dir: %w", err)
	}

	// Canonical portable media keys already include their public namespace.
	// Retain the legacy uploads prefix for existing non-canonical callers.
	if relDir == "media/v1" || strings.HasPrefix(relDir, "media/v1/") {
		input.Dir = relDir
	} else {
		input.Dir = filestorage.DirPersistPath(relDir)
	}

	return s.saveFile(ctx, input, true)
}

func (s *Storage) SaveTemp(ctx context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
	relDir, err := sanitizeRelativePath(input.Dir)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: dir: %w", err)
	}

	input.Dir = filestorage.DirTemptPath(relDir)

	return s.saveFile(ctx, input, false)
}

func (s *Storage) Commit(_ context.Context, input filestorage.CommitInput) (filestorage.StoredFile, error) {
	if err := input.Validate(); err != nil {
		return filestorage.StoredFile{}, err
	}

	tempPath, err := sanitizeRelativePath(input.Path)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: temp path: %w", err)
	}

	destDir, err := sanitizeRelativePath(input.DestDir)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: dest dir: %w", err)
	}

	fileName, err := sanitizeFileName(input.FileName)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: file name: %w", err)
	}

	absTempPath := filepath.Join(s.rootDir, filepath.FromSlash(tempPath))
	absDestDir := filepath.Join(s.rootDir, filepath.FromSlash(destDir))
	if err := os.MkdirAll(absDestDir, 0o755); err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: mkdir dest dir: %w", err)
	}

	destPath := path.Join(destDir, fileName)
	absDestPath := filepath.Join(s.rootDir, filepath.FromSlash(destPath))

	if err := moveFile(absTempPath, absDestPath); err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: move file: %w", err)
	}

	s.cleanupParent(tempPath)

	info, err := os.Stat(absDestPath)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: stat dest: %w", err)
	}

	destPath = toSlash(destPath)

	return filestorage.StoredFile{
		RelativePath: destPath,
		URL:          s.buildURL(destPath),
		Size:         info.Size(),
		MimeType:     "",
	}, nil
}

func (s *Storage) Open(_ context.Context, relativePath string) (io.ReadCloser, error) {
	relPath, err := sanitizeRelativePath(relativePath)
	if err != nil {
		return nil, fmt.Errorf("local storage: open path: %w", err)
	}

	absPath := filepath.Join(s.rootDir, filepath.FromSlash(relPath))

	file, err := os.Open(absPath)
	if err != nil {
		return nil, fmt.Errorf("local storage: open file: %w", err)
	}

	return file, nil
}

func (s *Storage) Delete(_ context.Context, relativePath string) error {
	relPath, err := sanitizeRelativePath(relativePath)
	if err != nil {
		return fmt.Errorf("local storage: delete path: %w", err)
	}

	return s.deleteRelative(relPath)
}

func (s *Storage) DeleteBatch(_ context.Context, relativePaths []string) error {
	if len(relativePaths) == 0 {
		return nil
	}

	seen := make(map[string]struct{}, len(relativePaths))
	for _, rawPath := range relativePaths {
		relPath, err := sanitizeRelativePath(rawPath)
		if err != nil {
			return fmt.Errorf("local storage: delete path: %w", err)
		}
		if _, exists := seen[relPath]; exists {
			continue
		}
		seen[relPath] = struct{}{}

		if err := s.deleteRelative(relPath); err != nil {
			return err
		}
	}

	return nil
}

func (s *Storage) saveFile(
	_ context.Context,
	input filestorage.SaveFileInput,
	replace bool,
) (filestorage.StoredFile, error) {
	if err := input.Validate(); err != nil {
		return filestorage.StoredFile{}, err
	}

	fileName, err := sanitizeFileName(input.FileName)
	if err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: file name: %w", err)
	}

	relPath := path.Join(input.Dir, fileName)
	absDir := filepath.Join(s.rootDir, filepath.FromSlash(input.Dir))
	if err := os.MkdirAll(absDir, 0o755); err != nil {
		return filestorage.StoredFile{}, fmt.Errorf("local storage: mkdir temp dir: %w", err)
	}

	absPath := filepath.Join(s.rootDir, filepath.FromSlash(relPath))
	written, err := writeFile(absDir, absPath, input.Reader, replace)
	if err != nil {
		return filestorage.StoredFile{}, err
	}

	relPath = toSlash(relPath)

	return filestorage.StoredFile{
		RelativePath: relPath,
		URL:          s.buildURL(relPath),
		Size:         written,
		MimeType:     input.MimeType,
	}, nil
}

func writeFile(absDir, absPath string, reader io.Reader, replace bool) (int64, error) {
	if !replace {
		file, err := os.OpenFile(absPath, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o644)
		if err != nil {
			return 0, fmt.Errorf("local storage: create temp file: %w", err)
		}

		written, copyErr := io.Copy(file, reader)
		closeErr := file.Close()
		if copyErr != nil || closeErr != nil {
			_ = os.Remove(absPath)
			if copyErr != nil {
				return 0, fmt.Errorf("local storage: write temp file: %w", copyErr)
			}
			return 0, fmt.Errorf("local storage: close temp file: %w", closeErr)
		}

		return written, nil
	}

	file, err := os.CreateTemp(absDir, ".gouploads-persist-*")
	if err != nil {
		return 0, fmt.Errorf("local storage: create persistent temp file: %w", err)
	}
	tempPath := file.Name()
	defer func() { _ = os.Remove(tempPath) }()

	if err := file.Chmod(0o644); err != nil {
		_ = file.Close()
		return 0, fmt.Errorf("local storage: chmod persistent temp file: %w", err)
	}
	written, copyErr := io.Copy(file, reader)
	closeErr := file.Close()
	if copyErr != nil {
		return 0, fmt.Errorf("local storage: write persistent temp file: %w", copyErr)
	}
	if closeErr != nil {
		return 0, fmt.Errorf("local storage: close persistent temp file: %w", closeErr)
	}
	if err := os.Rename(tempPath, absPath); err != nil {
		return 0, fmt.Errorf("local storage: replace persistent file: %w", err)
	}

	return written, nil
}

func (s *Storage) cleanupParent(relPath string) {
	dir := path.Dir(relPath)
	for dir != "." && dir != "" && dir != "/" {
		absDir := filepath.Join(s.rootDir, filepath.FromSlash(dir))
		empty, err := isDirEmpty(absDir)
		if err != nil || !empty {
			return
		}
		_ = os.Remove(absDir)
		dir = path.Dir(dir)
	}
}

func (s *Storage) deleteRelative(relPath string) error {
	absPath := filepath.Join(s.rootDir, filepath.FromSlash(relPath))
	if err := os.Remove(absPath); err != nil && !errors.Is(err, os.ErrNotExist) {
		return fmt.Errorf("local storage: delete file: %w", err)
	}

	s.cleanupParent(relPath)
	return nil
}

func (s *Storage) buildURL(relPath string) string {
	if s.baseURL == "" || s.baseURL == "/" {
		return "/" + strings.TrimLeft(relPath, "/")
	}

	return s.baseURL + "/" + strings.TrimLeft(relPath, "/")
}

func sanitizeRelativePath(p string) (string, error) {
	value := strings.TrimSpace(strings.ReplaceAll(p, "\\", "/"))
	if value == "" {
		return "", errors.New("empty path")
	}

	clean := path.Clean(value)
	clean = strings.TrimPrefix(clean, "./")
	if clean == "." || clean == "" {
		return "", errors.New("path resolves to root")
	}
	if strings.HasPrefix(clean, "../") || clean == ".." {
		return "", errors.New("path escapes root")
	}

	return clean, nil
}

func sanitizeFileName(name string) (string, error) {
	clean := strings.TrimSpace(name)
	if clean == "" {
		return "", errors.New("empty file name")
	}
	if strings.Contains(clean, "/") || strings.Contains(clean, "\\") {
		return "", errors.New("file name contains path separators")
	}
	clean = path.Clean(clean)
	if clean == "." || clean == ".." {
		return "", errors.New("invalid file name")
	}
	return clean, nil
}

func moveFile(sourcePath, destPath string) error {
	inputFile, err := os.Open(sourcePath)
	if err != nil {
		return fmt.Errorf("open source file: %w", err)
	}
	defer inputFile.Close()

	outputFile, err := os.Create(destPath)
	if err != nil {
		return fmt.Errorf("create dest file: %w", err)
	}
	defer outputFile.Close()

	_, err = io.Copy(outputFile, inputFile)
	if err != nil {
		return fmt.Errorf("copy file: %w", err)
	}

	err = os.Remove(sourcePath)
	if err != nil {
		return fmt.Errorf("remove source file: %w", err)
	}

	return nil
}

func toSlash(p string) string {
	return strings.TrimLeft(filepath.ToSlash(p), "/")
}

func isDirEmpty(path string) (bool, error) {
	entries, err := os.ReadDir(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return true, nil
		}
		return false, err
	}
	return len(entries) == 0, nil
}
