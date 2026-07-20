//nolint:testpackage // white-box coverage verifies exact RawPath/RawQuery preservation.
package sourceurl

import (
	"context"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	uploadconfig "github.com/assurrussa/gouploads/config"
)

func TestS3ResolverPresignsDirectStagingOrigin(t *testing.T) {
	resolver, err := New(uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
		Public: uploadconfig.StoragePublicConfig{BaseURL: "https://media.example.test"},
		S3: uploadconfig.StorageS3Config{
			Endpoint:       "https://storage.example.test",
			SourceURLTTL:   15 * time.Minute,
			Region:         "test-region-1",
			Bucket:         "public-media",
			StagingBucket:  "private-media",
			AccessKey:      "test-access-key",
			SecretKey:      "test-secret-key",
			ForcePathStyle: true,
		},
		Tus: uploadconfig.StorageTusConfig{StagingPrefix: "staging/v1/tus"},
	})
	require.NoError(t, err)

	got, err := resolver.Resolve(
		context.Background(),
		"staging/v1/tus/session-1/source.jpg",
	)
	require.NoError(t, err)

	parsed, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, "https", parsed.Scheme)
	require.Equal(t, "storage.example.test", parsed.Host)
	require.Equal(t, "/private-media/staging/v1/tus/session-1/source.jpg", parsed.Path)
	require.Equal(t, "900", parsed.Query().Get("X-Amz-Expires"))
	require.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))
	require.Equal(t, "host", parsed.Query().Get("X-Amz-SignedHeaders"))
}

func TestS3ResolverUsesPublicBucketWhenStagingBucketIsEmpty(t *testing.T) {
	resolver, err := New(uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
		Public: uploadconfig.StoragePublicConfig{BaseURL: "https://media.example.test"},
		S3: uploadconfig.StorageS3Config{
			Endpoint:       "https://storage.example.test",
			SourceURLTTL:   time.Hour,
			Region:         "test-region-1",
			Bucket:         "private-media",
			AccessKey:      "test-access-key",
			SecretKey:      "test-secret-key",
			ForcePathStyle: true,
		},
	})
	require.NoError(t, err)

	got, err := resolver.Resolve(context.Background(), "private-media/staging/v1/tus/session/image.jpg")
	require.NoError(t, err)
	parsed, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, "storage.example.test", parsed.Host)
	require.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))
}

func TestLocalResolverKeepsExistingURL(t *testing.T) {
	resolver, err := New(uploadconfig.StorageConfig{Driver: uploadconfig.StorageDriverLocal})
	require.NoError(t, err)

	const source = "http://backend:8080/uploads/image.jpg?cache=1"
	got, err := resolver.Resolve(context.Background(), source)
	require.NoError(t, err)
	require.Equal(t, source, got)
}

func TestS3ResolverValidatesTTLAndAppliesDefault(t *testing.T) {
	base := uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
		Public: uploadconfig.StoragePublicConfig{BaseURL: "https://media.example.test"},
		S3: uploadconfig.StorageS3Config{
			Endpoint:       "https://storage.example.test",
			SourceURLTTL:   DefaultS3URLTTL,
			Region:         "test-region-1",
			Bucket:         "private-media",
			AccessKey:      "test-access-key",
			SecretKey:      "test-secret-key",
			ForcePathStyle: true,
		},
		Tus: uploadconfig.StorageTusConfig{StagingPrefix: "staging/v1/tus"},
	}

	invalidTTL := base
	invalidTTL.S3.SourceURLTTL = MaxS3URLTTL + time.Second
	_, err := New(invalidTTL)
	require.ErrorContains(t, err, "TTL")

	defaultTTL := base
	defaultTTL.S3.SourceURLTTL = 0
	resolver, err := New(defaultTTL)
	require.NoError(t, err)
	require.NotNil(t, resolver)
}

func TestS3ResolverRejectsSourceOutsideStagingPrefix(t *testing.T) {
	t.Parallel()

	resolver, err := New(uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
		Public: uploadconfig.StoragePublicConfig{BaseURL: "https://media.example.test"},
		S3: uploadconfig.StorageS3Config{
			Endpoint:     "https://storage.example.test",
			Region:       "test-region-1",
			Bucket:       "media",
			AccessKey:    "test-access-key",
			SecretKey:    "test-secret-key",
			SourceURLTTL: time.Hour,
		},
		Tus: uploadconfig.StorageTusConfig{StagingPrefix: "staging/v1/tus"},
	})
	require.NoError(t, err)

	_, err = resolver.Resolve(context.Background(), "media/v1/post/1/main.webp")
	require.ErrorContains(t, err, "outside the configured staging prefix")
}
