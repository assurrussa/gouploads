package host_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestObjectIDPtr(t *testing.T) {
	got := host.ObjectIDPtr(42)

	require.NotNil(t, got)
	require.EqualValues(t, 42, got.Int64())
	require.Equal(t, "42", got.String())
}

func TestFilesBaseURLAndBucket(t *testing.T) {
	cfg := host.StorageConfig{
		Driver:       host.StorageDriverS3,
		AppDomainURL: "https://app.example.test",
		S3: host.StorageS3Config{
			Host:   "cdn.example.test/",
			Bucket: "/media/",
		},
	}

	require.Equal(t, "https://cdn.example.test", host.FilesBaseURL(cfg))
	require.Equal(t, "media", host.FilesBucket(cfg))
	require.Equal(
		t,
		"https://cdn.example.test/media/image.jpg",
		host.ComposeFileURL(host.FilesBaseURL(cfg), host.FilesBucket(cfg), "image.jpg"),
	)
}

func TestNewCleanTusRequest(t *testing.T) {
	before := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	require.Equal(t, host.CleanTusRequest{Before: before}, host.NewCleanTusRequest(before))
}

func TestNewTusStoreRequiresDatabaseForS3(t *testing.T) {
	store, err := host.NewTusStore(host.StorageConfig{
		Driver: host.StorageDriverS3,
		Tus: host.StorageTusConfig{
			PartSize:         host.ParseSize("5MB"),
			LeaseTTL:         30 * time.Second,
			SessionTTL:       time.Hour,
			QuarantinePrefix: "quarantine/uploads",
		},
	}, nil)

	require.Nil(t, store)
	require.ErrorContains(t, err, "database is required")
	require.Error(t, host.ErrTusUploadBusy)
	require.Equal(t, host.TusStatusActive, host.TusStatus("active"))
}

func TestAfterProcessPayloadHelpers(t *testing.T) {
	userID := host.NewUserID()

	payload := host.NewAfterProcessPayload(
		userID,
		host.UserTypeAdmin,
		42,
		"admin_preview_attach",
		map[string]any{"sessionId": "session-1"},
	)

	raw, err := host.MarshalAfterProcessPayload(payload)
	require.NoError(t, err)

	got, err := host.UnmarshalAfterProcessPayload(raw)
	require.NoError(t, err)
	require.Equal(t, userID, got.UserID)
	require.Equal(t, host.UserTypeAdmin, got.UserType)
	require.Equal(t, int64(42), got.FileID)
	require.Equal(t, "admin_preview_attach", got.EventType)
	require.Equal(t, "session-1", got.Meta["sessionId"])
}

func TestEventConstructorsExposeStableHostTypes(t *testing.T) {
	uploadStatus := host.NewFileUploadStatusEvent(10, host.FileUploadTaskStatusCompleted)
	require.Equal(t, host.EventTypeUploadStatus, uploadStatus.EventName())

	afterProcess := host.NewEventAfterProcess(11, "image.jpg", host.StatusCompleted, "avatar")
	require.Equal(t, host.EventTypeAfterProcess, afterProcess.EventName())

	deleted := host.NewFileDeletedEvent(12, "old.jpg", host.FileDeleteStatusCompleted)
	require.Equal(t, host.EventTypeDeleted, deleted.EventName())
}

func TestBuildListenResizeRequest(t *testing.T) {
	expireAt := time.Date(2026, 5, 7, 13, 0, 0, 0, time.UTC)
	timestamp := time.Date(2026, 5, 7, 12, 0, 0, 0, time.UTC)

	got := host.BuildListenResizeRequest(host.ListenResizeInput{
		ExternalID: 10,
		Status:     "done",
		Attempt:    -3,
		Error:      "ignored",
		Metadata:   map[string]any{"source": "test"},
		Timestamp:  timestamp,
		Artifacts: []host.ResizeArtifactInput{
			{
				Preset:      "main",
				URL:         "https://cdn.example.test/main.jpg",
				MediaType:   "image",
				ContentType: "image/jpeg",
				Size:        10,
				ExpireAt:    expireAt,
			},
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
	require.Equal(t, host.ResizeArtifact{
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
	handler := host.NewUploadHandler(
		nil,
		nil,
		nil,
		nil,
		func(_ context.Context, metadata map[string]string) (host.UploadContext, error) {
			return host.UploadContext{UserID: 10, Metadata: metadata}, nil
		},
		nil,
	)

	require.NotPanics(t, func() {
		handler.RegisterStrategy("avatar", hostStrategy{})
	})
}

type hostStrategy struct{}

func (hostStrategy) CanUpload(context.Context, host.UploadContext) error {
	return nil
}

func (hostStrategy) GetConfig(context.Context, host.UploadContext) *host.FileUploadConfig {
	return nil
}

func (hostStrategy) GetAfterJobs(context.Context, host.UploadContext) ([]host.FileEventAfterJob, error) {
	return nil, nil
}
