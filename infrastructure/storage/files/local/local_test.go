package local_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/local"
)

func TestStorage_Exists(t *testing.T) {
	tmpDir, err := os.MkdirTemp("", "test-storage")
	require.NoError(t, err)
	defer os.RemoveAll(tmpDir)

	storage, err := local.New(tmpDir, "/uploads")
	require.NoError(t, err)

	// Test case 1: File exists
	filePath := filepath.Join(tmpDir, "test-dir", "test-file.txt")
	err = os.MkdirAll(filepath.Dir(filePath), 0o755)
	require.NoError(t, err)
	_, err = os.Create(filePath)
	require.NoError(t, err)

	exist, err := storage.Exists(context.Background(), filestorage.ExistFileInput{
		Path: "test-dir/test-file.txt",
	})
	require.NoError(t, err)
	require.True(t, exist.Exist)

	// Test case 2: File does not exist
	exist, err = storage.Exists(context.Background(), filestorage.ExistFileInput{
		Path: "test-dir/non-existent-file.txt",
	})
	require.NoError(t, err)
	require.False(t, exist.Exist)
}
