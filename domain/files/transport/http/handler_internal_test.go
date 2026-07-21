package http

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
)

func TestUploadedFileFromCompleteResultUsesPhysicalStorageName(t *testing.T) {
	t.Parallel()

	const storagePath = "staging/v1/tus/session-123/source.webp"
	uploaded, err := uploadedFileFromCompleteResult(tusupload.CompleteResult{
		RelativePath: storagePath,
		OriginalName: "client-avatar-name.webp",
		FileName:     "client-avatar-name.webp",
		Size:         42,
		MimeType:     "image/webp",
	})
	require.NoError(t, err)
	require.Equal(t, storagePath, uploaded.Path)
	require.Equal(t, "staging/v1/tus/session-123", uploaded.FolderPath)
	require.Equal(t, "source.webp", uploaded.FileName)
	require.Equal(t, "client-avatar-name.webp", uploaded.OriginalName)
}

func TestUploadedFileFromCompleteResultRejectsUnsafeStoragePath(t *testing.T) {
	t.Parallel()

	_, err := uploadedFileFromCompleteResult(tusupload.CompleteResult{
		RelativePath: "staging/v1/tus/../source.webp",
		OriginalName: "client.webp",
	})
	require.Error(t, err)
}
