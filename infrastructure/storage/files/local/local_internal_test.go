package local

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

var errReaderFailed = errors.New("reader failed")

type failingReader struct {
	data []byte
}

func (r *failingReader) Read(data []byte) (int, error) {
	if len(r.data) == 0 {
		return 0, errReaderFailed
	}

	written := copy(data, r.data)
	r.data = r.data[written:]
	return written, nil
}

func TestStorage_SaveTempAndCommit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	storage, err := New(root, "/static")
	require.NoError(t, err)

	temp, err := storage.SaveTemp(ctx, filestorage.SaveFileInput{
		Dir:      filestorage.FolderPrefixPathPersist.String() + "/entity/folder/custom/1",
		FileName: "example.txt",
		Size:     int64(len("hello")),
		MimeType: "text/plain",
		Reader:   strings.NewReader("hello"),
	})
	require.NoError(t, err)
	require.Equal(t, "/static/tmp/uploads/entity/folder/custom/1/example.txt", temp.URL)

	tempAbs := filepath.Join(root, filepath.FromSlash(temp.RelativePath))
	data, err := os.ReadFile(tempAbs)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))

	stored, err := storage.Commit(ctx, filestorage.CommitInput{
		Path:     temp.RelativePath,
		DestDir:  "uploads/final",
		FileName: "final.txt",
	})
	require.NoError(t, err)
	require.Equal(t, "/static/uploads/final/final.txt", stored.URL)

	_, err = os.Stat(tempAbs)
	require.ErrorIs(t, err, os.ErrNotExist)

	finalAbs := filepath.Join(root, filepath.FromSlash(stored.RelativePath))
	finalData, err := os.ReadFile(finalAbs)
	require.NoError(t, err)
	require.Equal(t, "hello", string(finalData))
}

func TestStorage_SavePersistAndCommit(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	storage, err := New(root, "/static")
	require.NoError(t, err)

	temp, err := storage.SavePersist(ctx, filestorage.SaveFileInput{
		Dir:      filestorage.FolderPrefixPathTemp.String() + "/entity/folder/custom/1",
		FileName: "example.txt",
		Size:     int64(len("hello")),
		MimeType: "text/plain",
		Reader:   strings.NewReader("hello"),
	})
	require.NoError(t, err)
	require.Equal(t, "/static/uploads/entity/folder/custom/1/example.txt", temp.URL)

	tempAbs := filepath.Join(root, filepath.FromSlash(temp.RelativePath))
	data, err := os.ReadFile(tempAbs)
	require.NoError(t, err)
	require.Equal(t, "hello", string(data))

	stored, err := storage.Commit(ctx, filestorage.CommitInput{
		Path:     temp.RelativePath,
		DestDir:  "uploads/final",
		FileName: "final.txt",
	})
	require.NoError(t, err)
	require.Equal(t, "/static/uploads/final/final.txt", stored.URL)

	_, err = os.Stat(tempAbs)
	require.ErrorIs(t, err, os.ErrNotExist)

	finalAbs := filepath.Join(root, filepath.FromSlash(stored.RelativePath))
	finalData, err := os.ReadFile(finalAbs)
	require.NoError(t, err)
	require.Equal(t, "hello", string(finalData))
}

func TestStorage_SavePersistAtomicallyReplacesExistingFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	storage, err := New(root, "")
	require.NoError(t, err)

	input := filestorage.SaveFileInput{
		Dir:      "media/v1/post/42/file",
		FileName: "main.webp",
		Reader:   strings.NewReader("first"),
	}
	first, err := storage.SavePersist(ctx, input)
	require.NoError(t, err)

	input.Reader = strings.NewReader("second")
	second, err := storage.SavePersist(ctx, input)
	require.NoError(t, err)
	require.Equal(t, first.RelativePath, second.RelativePath)

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(second.RelativePath)))
	require.NoError(t, err)
	require.Equal(t, "second", string(data))
}

func TestStorage_SavePersistFailureKeepsExistingFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	storage, err := New(root, "")
	require.NoError(t, err)

	stored, err := storage.SavePersist(ctx, filestorage.SaveFileInput{
		Dir:      "media/v1/post/42/file",
		FileName: "main.webp",
		Reader:   strings.NewReader("stable"),
	})
	require.NoError(t, err)

	_, err = storage.SavePersist(ctx, filestorage.SaveFileInput{
		Dir:      "media/v1/post/42/file",
		FileName: "main.webp",
		Reader:   &failingReader{data: []byte("partial")},
	})
	require.ErrorIs(t, err, errReaderFailed)

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(stored.RelativePath)))
	require.NoError(t, err)
	require.Equal(t, "stable", string(data))
}

func TestStorage_SaveTempRejectsExistingFile(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	storage, err := New(root, "")
	require.NoError(t, err)

	input := filestorage.SaveFileInput{
		Dir:      "post/42/session",
		FileName: "source.webp",
		Reader:   strings.NewReader("first"),
	}
	_, err = storage.SaveTemp(ctx, input)
	require.NoError(t, err)

	input.Reader = strings.NewReader("second")
	_, err = storage.SaveTemp(ctx, input)
	require.ErrorContains(t, err, "file exists")
}

func TestStorage_DeleteCleansUp(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	storage, err := New(root, "")
	require.NoError(t, err)

	temp, err := storage.SaveTemp(ctx, filestorage.SaveFileInput{
		Dir:      filestorage.FolderPrefixPathTemp.String() + "/entity/folder/custom/1",
		FileName: "cleanup.txt",
		Reader:   strings.NewReader("data"),
	})
	require.NoError(t, err)

	stored, err := storage.Commit(ctx, filestorage.CommitInput{
		Path:     temp.RelativePath,
		DestDir:  "uploads/cleanup",
		FileName: "cleanup.txt",
	})
	require.NoError(t, err)

	err = storage.Delete(ctx, stored.RelativePath)
	require.NoError(t, err)

	_, err = os.Stat(filepath.Join(root, filepath.FromSlash(stored.RelativePath)))
	require.ErrorIs(t, err, os.ErrNotExist)

	_, dirErr := os.Stat(filepath.Join(root, "uploads", "cleanup"))
	require.ErrorIs(t, dirErr, os.ErrNotExist)
}

func TestStorage_Open(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	storage, err := New(root, "")
	require.NoError(t, err)

	temp, err := storage.SaveTemp(ctx, filestorage.SaveFileInput{
		Dir:      filestorage.FolderPrefixPathTemp.String() + "/entity/folder/custom/1",
		FileName: "note.txt",
		Reader:   strings.NewReader("hello"),
	})
	require.NoError(t, err)

	stored, err := storage.Commit(ctx, filestorage.CommitInput{
		Path:     temp.RelativePath,
		DestDir:  "uploads/files",
		FileName: "note.txt",
	})
	require.NoError(t, err)

	file, err := storage.Open(ctx, stored.RelativePath)
	require.NoError(t, err)
	data, readErr := io.ReadAll(file)
	_ = file.Close()
	require.NoError(t, readErr)
	require.Equal(t, "hello", string(data))

	_, err = storage.Open(ctx, "../outside.txt")
	require.Error(t, err)
	require.Contains(t, err.Error(), "open path")
}

func TestStorage_SaveTempInvalidDir(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()

	storage, err := New(root, "")
	require.NoError(t, err)

	_, err = storage.SaveTemp(ctx, filestorage.SaveFileInput{
		Dir:      "../escape",
		FileName: "bad.txt",
		Reader:   strings.NewReader("data"),
	})
	require.Error(t, err)
}

func TestSanitizeRelativePath_Valid(t *testing.T) {
	got, err := sanitizeRelativePath(" tmp/../tmp/uploads/example.txt ")
	require.NoError(t, err)
	require.Equal(t, "tmp/uploads/example.txt", got)
}

func TestSanitizeRelativePath_EscapesRoot(t *testing.T) {
	_, err := sanitizeRelativePath("../etc/passwd")
	require.Error(t, err)
	require.Contains(t, err.Error(), "path escapes root")
}

func TestSanitizeRelativePath_ResolvesToRoot(t *testing.T) {
	_, err := sanitizeRelativePath("./")
	require.Error(t, err)
	require.Contains(t, err.Error(), "path resolves to root")
}

func TestSanitizeFileName_Valid(t *testing.T) {
	got, err := sanitizeFileName("  Report .PDF  ")
	require.NoError(t, err)
	require.Equal(t, "Report .PDF", got)
}

func TestSanitizeFileName_Invalid(t *testing.T) {
	_, err := sanitizeFileName("../secret.txt")
	require.Error(t, err)
}

func TestToSlash(t *testing.T) {
	got := toSlash("/tmp/uploads/file.txt")
	require.Equal(t, "tmp/uploads/file.txt", got)
}

func TestIsDirEmpty(t *testing.T) {
	tdir := t.TempDir()

	empty, err := isDirEmpty(tdir)
	require.NoError(t, err)
	require.True(t, empty)

	filePath := filepath.Join(tdir, "file.txt")
	require.NoError(t, os.WriteFile(filePath, []byte("data"), 0o600))

	empty, err = isDirEmpty(tdir)
	require.NoError(t, err)
	require.False(t, empty)
}
