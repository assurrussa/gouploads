package filesanitize_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/internal/filesanitize"
)

func TestStoragePaths(t *testing.T) {
	t.Parallel()
	got, err := filesanitize.EnsureRelativePath(` /temp\images/file.png `)
	require.NoError(t, err)
	require.Equal(t, "temp/images/file.png", got)
	for _, path := range []string{"", "/", "../file", "folder/../file", "folder/./file", "folder//file", "folder/"} {
		_, err := filesanitize.EnsureRelativePath(path)
		require.Error(t, err, path)
	}
	got, err = filesanitize.BuildSafePath(" Main Images ", "", "Sub--folder")
	require.NoError(t, err)
	require.Equal(t, "main-images/sub-folder", got)
	for _, name := range []string{"../file", "file/name", `file\name`, "image..png"} {
		_, err := filesanitize.SanitizeFileName(name)
		require.Error(t, err, name)
	}
}
