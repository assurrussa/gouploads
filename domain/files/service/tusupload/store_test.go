//nolint:testpackage // need package internals to verify on-disk metadata handling
package tusupload

import (
	"bytes"
	"context"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestFileStore_AppendComplete(t *testing.T) {
	t.Parallel()

	store, err := NewFileStore(t.TempDir())
	require.NoError(t, err)

	data := []byte("hello tus")
	session, err := store.Create(context.Background(), CreateRequest{
		UploadLength: int64(len(data)),
		OriginalName: "photo.png",
		FileName:     "photo.png",
	})
	require.NoError(t, err)

	offset, err := store.Append(context.Background(), session.ID, 0, data, "image/png")
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), offset)

	loaded, err := store.Get(context.Background(), session.ID)
	require.NoError(t, err)
	require.Equal(t, "image/png", loaded.MimeType)

	result, err := store.Complete(context.Background(), session.ID)
	require.NoError(t, err)
	require.Equal(t, int64(len(data)), result.Size)
	require.NotNil(t, result.Reader)
	defer result.Reader.Close()

	got, err := io.ReadAll(result.Reader)
	require.NoError(t, err)
	require.Equal(t, data, got)
}

func TestFileStore_AppendOffsetMismatch(t *testing.T) {
	t.Parallel()

	store, err := NewFileStore(t.TempDir())
	require.NoError(t, err)

	session, err := store.Create(context.Background(), CreateRequest{
		UploadLength: 4,
		OriginalName: "doc.pdf",
		FileName:     "doc.pdf",
	})
	require.NoError(t, err)

	offset, err := store.Append(context.Background(), session.ID, 1, []byte("data"), "")
	require.ErrorIs(t, err, ErrOffsetMismatch)
	require.Equal(t, int64(0), offset)
}

func TestFileStore_Cleanup(t *testing.T) {
	t.Parallel()

	root := t.TempDir()
	store, err := NewFileStore(root)
	require.NoError(t, err)

	session, err := store.Create(context.Background(), CreateRequest{
		UploadLength: 3,
		OriginalName: "old.txt",
		FileName:     "old.txt",
	})
	require.NoError(t, err)

	metaPath := filepath.Join(root, session.ID, "meta.json")
	require.NoError(t, updateMetaUpdatedAt(metaPath, time.Now().Add(-2*time.Hour)))

	removed, err := store.Cleanup(context.Background(), time.Now().Add(-time.Hour))
	require.NoError(t, err)
	require.Equal(t, 1, removed)

	_, err = store.Get(context.Background(), session.ID)
	require.ErrorIs(t, err, ErrNotFound)

	_, statErr := os.Stat(filepath.Join(root, session.ID))
	require.Error(t, statErr)
}

func TestFileStore_AppendLengthExceeded(t *testing.T) {
	t.Parallel()

	store, err := NewFileStore(t.TempDir())
	require.NoError(t, err)

	session, err := store.Create(context.Background(), CreateRequest{
		UploadLength: 2,
		OriginalName: "clip.mp4",
		FileName:     "clip.mp4",
	})
	require.NoError(t, err)

	offset, err := store.Append(context.Background(), session.ID, 0, bytes.Repeat([]byte("a"), 4), "")
	require.ErrorIs(t, err, ErrLengthExceeded)
	require.Equal(t, int64(0), offset)
}
