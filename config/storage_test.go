package config_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	uploadconfig "github.com/assurrussa/gouploads/config"
)

func TestNormalizeStorageConfigAppliesPortableS3Defaults(t *testing.T) {
	t.Parallel()

	cfg, err := uploadconfig.NormalizeStorageConfig(uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
		Public: uploadconfig.StoragePublicConfig{BaseURL: "https://media.example.test/"},
		S3: uploadconfig.StorageS3Config{
			Endpoint:  "https://s3.example.test/",
			Region:    "region-1",
			Bucket:    "media.example.test",
			AccessKey: "access",
			SecretKey: "secret",
		},
	})
	require.NoError(t, err)
	require.Equal(t, "https://media.example.test", cfg.Public.BaseURL)
	require.Equal(t, uploadconfig.DefaultPublicPrefix, cfg.Public.Prefix)
	require.Equal(t, uploadconfig.DefaultStagingPrefix, cfg.Tus.StagingPrefix)
	require.Equal(t, cfg.S3.Bucket, cfg.S3.StagingBucket)
	require.Equal(t, 15*time.Minute, cfg.S3.SourceURLTTL)
	require.Equal(t, 30*time.Second, cfg.S3.Timeout)
	require.Equal(t, 10, cfg.S3.MaxRetries)
}

func TestNormalizeStorageConfigRejectsInvalidS3Contract(t *testing.T) {
	t.Parallel()

	valid := uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
		Public: uploadconfig.StoragePublicConfig{BaseURL: "https://media.example.test"},
		S3: uploadconfig.StorageS3Config{
			Endpoint:  "https://s3.example.test",
			Region:    "region-1",
			Bucket:    "media",
			AccessKey: "access",
			SecretKey: "secret",
		},
	}

	tests := []struct {
		name   string
		mutate func(*uploadconfig.StorageConfig)
		want   string
	}{
		{
			name:   "endpoint scheme",
			mutate: func(cfg *uploadconfig.StorageConfig) { cfg.S3.Endpoint = "s3.example.test" },
			want:   "HTTP or HTTPS",
		},
		{name: "public URL", mutate: func(cfg *uploadconfig.StorageConfig) { cfg.Public.BaseURL = "" }, want: "HTTP or HTTPS"},
		{name: "bucket path", mutate: func(cfg *uploadconfig.StorageConfig) { cfg.S3.Bucket = "media/path" }, want: "bucket name"},
		{name: "public prefix", mutate: func(cfg *uploadconfig.StorageConfig) { cfg.Public.Prefix = "../media" }, want: "confined"},
		{
			name:   "staging prefix",
			mutate: func(cfg *uploadconfig.StorageConfig) { cfg.Tus.StagingPrefix = "/staging" },
			want:   "relative",
		},
		{
			name: "overlapping prefixes",
			mutate: func(cfg *uploadconfig.StorageConfig) {
				cfg.Public.Prefix = "media/v1"
				cfg.Tus.StagingPrefix = "media/v1/tus"
			},
			want: "must not overlap",
		},
		{name: "credentials", mutate: func(cfg *uploadconfig.StorageConfig) { cfg.S3.SecretKey = "" }, want: "secret key"},
		{
			name:   "timeout",
			mutate: func(cfg *uploadconfig.StorageConfig) { cfg.S3.Timeout = time.Millisecond },
			want:   "between 1s and 5m",
		},
		{name: "retries", mutate: func(cfg *uploadconfig.StorageConfig) { cfg.S3.MaxRetries = 51 }, want: "between 1 and 50"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			cfg := valid
			test.mutate(&cfg)
			_, err := uploadconfig.NormalizeStorageConfig(cfg)
			require.ErrorContains(t, err, test.want)
		})
	}
}
