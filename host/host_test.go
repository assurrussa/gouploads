package host

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestObjectIDPtr(t *testing.T) {
	got := ObjectIDPtr(42)

	require.NotNil(t, got)
	require.EqualValues(t, 42, got.Int64())
	require.Equal(t, "42", got.String())
}

func TestFilesBaseURLAndBucket(t *testing.T) {
	cfg := StorageConfig{
		Driver:       StorageDriverS3,
		AppDomainURL: "https://app.example.test",
		S3: StorageS3Config{
			Host:   "cdn.example.test/",
			Bucket: "/media/",
		},
	}

	require.Equal(t, "https://cdn.example.test", FilesBaseURL(cfg))
	require.Equal(t, "media", FilesBucket(cfg))
	require.Equal(t, "https://cdn.example.test/media/image.jpg", ComposeFileURL(FilesBaseURL(cfg), FilesBucket(cfg), "image.jpg"))
}

func TestNewCleanTusRequest(t *testing.T) {
	before := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	require.Equal(t, CleanTusRequest{Before: before}, NewCleanTusRequest(before))
}

func TestBuildListenResizeRequest(t *testing.T) {
	expireAt := time.Date(2026, 5, 7, 13, 0, 0, 0, time.UTC)
	timestamp := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	got := BuildListenResizeRequest(ListenResizeInput{
		ExternalID: 10,
		Status:     "done",
		Attempt:    -3,
		Error:      "ignored",
		Metadata:   map[string]any{"source": "test"},
		Timestamp:  timestamp,
		Artifacts: []ResizeArtifactInput{
			{Preset: "main", URL: "https://cdn.example.test/main.jpg", MediaType: "image", ContentType: "image/jpeg", Size: 10, ExpireAt: expireAt},
			{Preset: "main", URL: "https://cdn.example.test/duplicate.jpg"},
			{Preset: "thumb", URL: "https://cdn.example.test/thumb.jpg"},
		},
	})

	require.EqualValues(t, 0, got.Attempt)
	require.Equal(t, int64(10), got.ExternalID)
	require.Equal(t, "done", got.Status)
	require.Equal(t, "ignored", got.Error)
	require.Equal(t, timestamp, got.Timestamp)
	require.Len(t, got.Artifacts, 2)
	require.Equal(t, ResizeArtifact{
		Preset:      "main",
		URL:         "https://cdn.example.test/main.jpg",
		MediaType:   "image",
		ContentType: "image/jpeg",
		Size:        10,
		ExpireAt:    expireAt,
	}, got.Artifacts[0])
	require.Equal(t, "thumb", got.Artifacts[1].Preset)
}

func TestUploadHandlerUsesHostOwnedStrategyContract(t *testing.T) {
	handler := NewUploadHandler(
		nil,
		nil,
		nil,
		nil,
		func(_ context.Context, metadata map[string]string) (UploadContext, error) {
			return UploadContext{UserID: 10, Metadata: metadata}, nil
		},
		nil,
	)

	require.NotPanics(t, func() {
		handler.RegisterStrategy("avatar", hostStrategy{})
	})
}

func TestInternalContextBuilderConvertsHostContext(t *testing.T) {
	builder := internalContextBuilder(func(_ context.Context, metadata map[string]string) (UploadContext, error) {
		return UploadContext{
			UserID:    10,
			SessionID: "session-id",
			Metadata:  metadata,
		}, nil
	})

	got, err := builder(context.Background(), map[string]string{"context": "avatar"})
	require.NoError(t, err)
	require.EqualValues(t, 10, got.UserID)
	require.Equal(t, "session-id", got.SessionID)
	require.Equal(t, map[string]string{"context": "avatar"}, got.Metadata)
}

type hostStrategy struct{}

func (hostStrategy) CanUpload(context.Context, UploadContext) error {
	return nil
}

func (hostStrategy) GetConfig(context.Context, UploadContext) *FileUploadConfig {
	return nil
}

func (hostStrategy) GetAfterJobs(context.Context, UploadContext) ([]FileEventAfterJob, error) {
	return nil, nil
}
