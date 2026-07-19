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

func TestS3ResolverPresignsAndRewritesOnlySchemeAndHost(t *testing.T) {
	resolver, err := New(uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
		S3: uploadconfig.StorageS3Config{
			Endpoint:       "https://storage.example.test",
			SourceHost:     "https://source-proxy.example.test:8443",
			SourceURLTTL:   6 * time.Hour,
			Region:         "test-region-1",
			Bucket:         "private-media",
			AccessKey:      "test-access-key",
			SecretKey:      "test-secret-key",
			ForcePathStyle: true,
		},
	})
	require.NoError(t, err)

	got, err := resolver.Resolve(
		context.Background(),
		"https://storage.example.test/private-media/quarantine/uploads/image.jpg",
	)
	require.NoError(t, err)

	parsed, err := url.Parse(got)
	require.NoError(t, err)
	require.Equal(t, "https", parsed.Scheme)
	require.Equal(t, "source-proxy.example.test:8443", parsed.Host)
	require.Equal(t, "/private-media/quarantine/uploads/image.jpg", parsed.Path)
	require.Equal(t, "21600", parsed.Query().Get("X-Amz-Expires"))
	require.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))
	require.Equal(t, "host", parsed.Query().Get("X-Amz-SignedHeaders"))
}

func TestRewriteURLHostPreservesSignedPathAndQuery(t *testing.T) {
	raw := "https://storage.example.test/private-media/a%2Fb.jpg?X-Amz-Algorithm=AWS4-HMAC-SHA256&X-Amz-Signature=abc%2Fdef"
	original, err := url.Parse(raw)
	require.NoError(t, err)

	got, err := rewriteURLHost(raw, "http://proxy.example.test:8080")
	require.NoError(t, err)
	rewritten, err := url.Parse(got)
	require.NoError(t, err)

	require.Equal(t, "http", rewritten.Scheme)
	require.Equal(t, "proxy.example.test:8080", rewritten.Host)
	require.Equal(t, original.Path, rewritten.Path)
	require.Equal(t, original.RawPath, rewritten.RawPath)
	require.Equal(t, original.RawQuery, rewritten.RawQuery)
}

func TestS3ResolverFallsBackToPresignedOriginURL(t *testing.T) {
	resolver, err := New(uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
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

	got, err := resolver.Resolve(context.Background(), "private-media/quarantine/uploads/image.jpg")
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

func TestS3ResolverValidatesTTLAndSourceHost(t *testing.T) {
	base := uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
		S3: uploadconfig.StorageS3Config{
			Endpoint:       "https://storage.example.test",
			SourceURLTTL:   DefaultS3URLTTL,
			Region:         "test-region-1",
			Bucket:         "private-media",
			AccessKey:      "test-access-key",
			SecretKey:      "test-secret-key",
			ForcePathStyle: true,
		},
	}

	invalidTTL := base
	invalidTTL.S3.SourceURLTTL = MaxS3URLTTL + time.Second
	_, err := New(invalidTTL)
	require.ErrorContains(t, err, "TTL")

	invalidHost := base
	invalidHost.S3.SourceHost = "https://proxy.example.test/prefix?token=secret"
	_, err = New(invalidHost)
	require.ErrorContains(t, err, "must not contain")

	defaultTTL := base
	defaultTTL.S3.SourceURLTTL = 0
	resolver, err := New(defaultTTL)
	require.NoError(t, err)
	require.NotNil(t, resolver)
}
