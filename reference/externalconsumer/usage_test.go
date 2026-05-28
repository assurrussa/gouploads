package externalconsumer_test

import (
	"context"
	"io/fs"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
)

func TestHostSurfaceCoversEmbeddingWorkflow(t *testing.T) {
	t.Helper()

	cfg := host.StorageConfig{
		Driver:       host.StorageDriverS3,
		AppDomainURL: "https://app.example.test",
		S3: host.StorageS3Config{
			Host:   "https://cdn.example.test",
			Bucket: "media",
		},
		Image: host.ImagePipelineConfig{
			ResizerHost:         "http://media-resizer:18085/jobs",
			WebhookCallbackHost: "http://host:8080/api/v1/media-resize",
			ResizerToken:        "image-token",
		},
		Video: host.VideoPipelineConfig{
			ResizerHost:         "http://media-resizer:18085/jobs",
			WebhookCallbackHost: "http://host:8080/api/v1/media-resize",
			ResizerToken:        "video-token",
		},
		Tus: host.StorageTusConfig{
			PartSize: host.ParseSize("8MB"),
		},
	}
	require.Equal(t, "https://cdn.example.test", host.FilesBaseURL(cfg))
	require.Equal(t, "media", host.FilesBucket(cfg))

	deps := host.BootstrapDependencies()
	require.NotEmpty(t, deps.List())

	handler := host.NewUploadHandler(
		nil,
		nil,
		nil,
		nil,
		func(_ context.Context, metadata map[string]string) (host.UploadContext, error) {
			return host.UploadContext{
				UserID:   10,
				UserUUID: host.NewUserID(),
				Metadata: metadata,
			}, nil
		},
		func(path string) string {
			return host.ComposeFileURL(host.FilesBaseURL(cfg), host.FilesBucket(cfg), path)
		},
	)
	require.NotNil(t, handler)
	require.NotPanics(t, func() {
		handler.RegisterStrategy("default", consumerStrategy{})
	})
	require.NotNil(t, host.NewFiberUploadHandler(handler))

	request := host.BuildListenResizeRequest(host.ListenResizeInput{
		ExternalID: 42,
		Status:     "completed",
		Artifacts: []host.ResizeArtifactInput{{
			Preset:      "main",
			URL:         "https://cdn.example.test/media/main.jpg",
			MediaType:   "image",
			ContentType: "image/jpeg",
			Size:        100,
		}},
	})
	require.EqualValues(t, 42, request.ExternalID)
	require.Len(t, request.Artifacts, 1)

	migrationFS, err := host.MigrationsFS()
	require.NoError(t, err)
	migrations, err := host.MigrationFiles()
	require.NoError(t, err)
	require.NotEmpty(t, migrations)
	_, err = fs.ReadFile(migrationFS, migrations[0])
	require.NoError(t, err)

	_ = host.OutboxJobDeps{}
	_ = host.NewFileRepo
	_ = host.MustFileRepo
	_ = hosttest.SaveFileInput{}
	_ = hosttest.NewListenResizeRequestMatcher
}

type consumerStrategy struct{}

func (consumerStrategy) CanUpload(context.Context, host.UploadContext) error {
	return nil
}

func (consumerStrategy) GetConfig(context.Context, host.UploadContext) *host.FileUploadConfig {
	return &host.FileUploadConfig{
		AllowedExtensions: []string{".jpg", ".jpeg", ".png", ".webp"},
		MaxFileSize:       10 * 1024 * 1024,
	}
}

func (consumerStrategy) GetAfterJobs(context.Context, host.UploadContext) ([]host.FileEventAfterJob, error) {
	return nil, nil
}
