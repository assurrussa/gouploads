package host_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestFiberUploadHandlerSplitsRouteGuards(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	handler := host.NewFiberUploadHandler(host.NewUploadHandler(nil, nil, nil, nil, nil, nil))
	handler.RegisterGroupRoutesWithGuards("/files", app, host.UploadRouteGuards{
		Read:   []fiber.Handler{rejectWithStatus(http.StatusUnauthorized)},
		Create: []fiber.Handler{rejectWithStatus(http.StatusPaymentRequired)},
		Delete: []fiber.Handler{rejectWithStatus(http.StatusForbidden)},
	})

	requireRouteStatus(t, app, http.MethodGet, "/files", http.StatusUnauthorized)
	requireRouteStatus(t, app, http.MethodPost, "/files", http.StatusPaymentRequired)
	requireRouteStatus(t, app, http.MethodPost, "/files/tus", http.StatusPaymentRequired)
	requireRouteStatus(t, app, http.MethodDelete, "/files/1", http.StatusForbidden)
}

func TestFiberUploadHandlerRegistersCMSOnlyTusSurface(t *testing.T) {
	t.Parallel()

	app := fiber.New()
	handler := host.NewFiberUploadHandler(host.NewUploadHandler(nil, nil, nil, nil, nil, nil))
	handler.RegisterCMSTusRoutes(
		"/admin/api/cms/v1/uploads",
		app,
		rejectWithStatus(http.StatusForbidden),
	)

	requireRouteStatus(t, app, http.MethodOptions, "/admin/api/cms/v1/uploads/tus", http.StatusForbidden)
	requireRouteStatus(t, app, http.MethodPost, "/admin/api/cms/v1/uploads/tus", http.StatusForbidden)
	requireRouteStatus(t, app, http.MethodHead, "/admin/api/cms/v1/uploads/tus/upload-1", http.StatusForbidden)
	requireRouteStatus(t, app, http.MethodPatch, "/admin/api/cms/v1/uploads/tus/upload-1", http.StatusForbidden)

	openApp := fiber.New()
	openHandler := host.NewFiberUploadHandler(host.NewUploadHandler(nil, nil, nil, nil, nil, nil))
	openHandler.RegisterCMSTusRoutes("/admin/api/cms/v1/uploads", openApp)
	requireRouteStatus(
		t,
		openApp,
		http.MethodPost,
		"/admin/api/cms/v1/uploads/tus/upload-1/complete",
		http.StatusNotFound,
	)
	requireRouteStatus(t, openApp, http.MethodGet, "/admin/api/cms/v1/uploads", http.StatusNotFound)
	requireRouteStatus(t, openApp, http.MethodDelete, "/admin/api/cms/v1/uploads/1", http.StatusNotFound)
}

func rejectWithStatus(status int) fiber.Handler {
	return func(c fiber.Ctx) error {
		return c.SendStatus(status)
	}
}

func requireRouteStatus(t *testing.T, app *fiber.App, method, target string, want int) {
	t.Helper()

	response, err := app.Test(
		httptest.NewRequestWithContext(t.Context(), method, target, nil),
	)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, response.Body.Close()) })
	require.Equal(t, want, response.StatusCode)
}

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
		Public: host.StoragePublicConfig{
			BaseURL: "https://media.example.test",
			Prefix:  "media/v1",
		},
		S3: host.StorageS3Config{
			Bucket: "/media/",
		},
	}

	require.Equal(t, "https://media.example.test", host.FilesBaseURL(cfg))
	require.Empty(t, host.FilesBucket(cfg))
	require.Equal(
		t,
		"https://media.example.test/image.jpg",
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
		Public: host.StoragePublicConfig{BaseURL: "https://media.example.test"},
		S3: host.StorageS3Config{
			Endpoint:  "https://s3.example.test",
			Region:    "region-1",
			Bucket:    "media",
			AccessKey: "access",
			SecretKey: "secret",
		},
		Tus: host.StorageTusConfig{
			PartSize:      host.ParseSize("5MB"),
			LeaseTTL:      30 * time.Second,
			SessionTTL:    time.Hour,
			StagingPrefix: "staging/v1/tus",
		},
	}, nil)

	require.Nil(t, store)
	require.ErrorContains(t, err, "database is required")
	require.Error(t, host.ErrTusUploadBusy)
	require.Equal(t, host.TusStatusActive, host.TusStatus("active"))
}

func TestNewStorageBuildsLocalFacadeAndRejectsUnknownDriver(t *testing.T) {
	t.Parallel()
	storage, err := host.NewStorage(host.StorageConfig{
		Driver: host.StorageDriverLocal,
		Local:  host.StorageLocalConfig{Root: t.TempDir(), BaseURL: "https://files.example.test"},
	})
	require.NoError(t, err)
	require.NotNil(t, storage)
	_, err = host.NewStorage(host.StorageConfig{Driver: "unknown"})
	require.ErrorContains(t, err, "unsupported storage driver")
}

func TestNewSourceURLResolverKeepsLocalSourceURL(t *testing.T) {
	t.Parallel()

	resolver, err := host.NewSourceURLResolver(host.StorageConfig{Driver: host.StorageDriverLocal})
	require.NoError(t, err)

	const source = "http://backend:8080/uploads/image.jpg?cache=1"
	got, err := resolver.Resolve(context.Background(), source)
	require.NoError(t, err)
	require.Equal(t, source, got)
}

func TestNewSourceURLResolverComposesLocalHTTPSourceURL(t *testing.T) {
	t.Parallel()

	resolver, err := host.NewSourceURLResolver(host.StorageConfig{
		Driver: host.StorageDriverLocal,
		Local: host.StorageLocalConfig{
			SourceBaseURL: "http://backend:8080",
		},
	})
	require.NoError(t, err)

	got, err := resolver.Resolve(context.Background(), "tmp/uploads/admin/42/source.webp")
	require.NoError(t, err)
	require.Equal(t, "http://backend:8080/tmp/uploads/admin/42/source.webp", got)
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
