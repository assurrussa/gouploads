package fileurl_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/shared/fileurl"
)

func TestBaseURLLocalAbsolute(t *testing.T) {
	cfg := config.StorageConfig{
		AppDomainURL: "https://admin.local",
		Driver:       config.StorageDriverLocal,
		Local:        config.StorageLocalConfig{BaseURL: "https://static.local"},
	}

	require.Equal(t, "https://static.local", fileurl.BaseURL(cfg))
}

func TestBaseURLLocalRelative(t *testing.T) {
	cfg := config.StorageConfig{
		AppDomainURL: "https://app.local",
		Driver:       config.StorageDriverLocal,
		Local:        config.StorageLocalConfig{BaseURL: "uploads/public"},
	}

	require.Equal(t, "https://app.local/uploads/public", fileurl.BaseURL(cfg))
}

func TestBaseURLS3PublicBase(t *testing.T) {
	cfg := config.StorageConfig{
		AppDomainURL: "https://app.local",
		Driver:       config.StorageDriverS3,
		Public:       config.StoragePublicConfig{BaseURL: "https://media.example"},
	}

	require.Equal(t, "https://media.example", fileurl.BaseURL(cfg))
}

func TestBaseURLS3DoesNotExposeEndpoint(t *testing.T) {
	cfg := config.StorageConfig{
		AppDomainURL: "https://app.local",
		Driver:       config.StorageDriverS3,
		S3:           config.StorageS3Config{Endpoint: "s3store.internal"},
	}

	require.Equal(t, "https://app.local", fileurl.BaseURL(cfg))
}

func TestBaseURLFallback(t *testing.T) {
	cfg := config.StorageConfig{
		AppDomainURL: "app.internal",
	}

	require.Equal(t, "https://app.internal", fileurl.BaseURL(cfg))
}

func TestBucketS3(t *testing.T) {
	cfg := config.StorageConfig{
		Driver: config.StorageDriverS3,
		S3:     config.StorageS3Config{Bucket: "/uploads/"},
	}

	require.Empty(t, fileurl.Bucket(cfg))
}

func TestBucketNonS3(t *testing.T) {
	cfg := config.StorageConfig{
		Driver: config.StorageDriverLocal,
	}

	require.Empty(t, fileurl.Bucket(cfg))
}

func TestCompose(t *testing.T) {
	t.Parallel()

	testCases := []struct {
		name   string
		base   string
		bucket string
		raw    string
		want   string
	}{
		{
			name: "absolute-url",
			base: "https://files.example",
			raw:  "https://cdn/image.png",
			want: "https://cdn/image.png",
		},
		{
			name:   "protocol-relative",
			base:   "https://files.example",
			raw:    "//cdn/image.png",
			want:   "https://cdn/image.png",
			bucket: "assets",
		},
		{
			name:   "with-bucket",
			base:   "https://files.example",
			bucket: "assets",
			raw:    "/uploads/photo.jpg",
			want:   "https://files.example/assets/uploads/photo.jpg",
		},
		{
			name:   "relative-path",
			base:   "https://files.example/",
			bucket: "assets",
			raw:    "uploads/photo.jpg",
			want:   "https://files.example/assets/uploads/photo.jpg",
		},
		{
			name:   "bucket-already-present",
			base:   "https://files.example",
			bucket: "assets",
			raw:    "/assets/uploads/photo.jpg",
			want:   "https://files.example/assets/uploads/photo.jpg",
		},
		{
			name:   "path-equals-bucket",
			base:   "https://files.example",
			bucket: "assets",
			raw:    "/assets",
			want:   "https://files.example/assets",
		},
		{
			name: "empty-bucket",
			base: "https://files.example",
			raw:  "/uploads/photo.jpg",
			want: "https://files.example/uploads/photo.jpg",
		},
		{
			name:   "no-base-host",
			base:   "",
			bucket: "assets",
			raw:    "/uploads/photo.jpg",
			want:   "/uploads/photo.jpg",
		},
		{
			name:   "empty-raw",
			base:   "https://files.example/static/",
			bucket: "assets",
			raw:    "",
			want:   "",
		},
		{
			name: "base-without-scheme",
			base: "files.example",
			raw:  "/image.png",
			want: "https://files.example/image.png",
		},
		{
			name: "relative-base-raw-empty",
			base: "",
			raw:  "",
			want: "",
		},
	}

	for _, tc := range testCases {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			require.Equal(t, tc.want, fileurl.Compose(tc.base, tc.bucket, tc.raw))
		})
	}
}
