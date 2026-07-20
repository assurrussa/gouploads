//go:build integration

package sourceurl

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"

	uploadconfig "github.com/assurrussa/gouploads/config"
)

func TestIntegrationPrivateS3SourceDirectPresignedGET(t *testing.T) {
	ctx := context.Background()
	endpoint := sourceTestEnv("TEST_S3_ENDPOINT", "http://localhost:9090")
	accessKey := sourceTestEnv("TEST_S3_ACCESS_KEY", "minioadmin")
	secretKey := sourceTestEnv("TEST_S3_SECRET_KEY", "minioadmin")
	region := sourceTestEnv("TEST_S3_REGION", "us-east-1")
	bucket := sourceTestEnv("TEST_S3_BUCKET", "source-url-integration-tests")

	client := sourceTestS3Client(endpoint, region, accessKey, secretKey)
	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := client.ListBuckets(pingCtx, &s3.ListBucketsInput{})
	require.NoError(t, err, "mandatory S3 integration dependency is unavailable")

	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		var owned *types.BucketAlreadyOwnedByYou
		var exists *types.BucketAlreadyExists
		if !errors.As(err, &owned) && !errors.As(err, &exists) {
			t.Fatalf("create bucket: %v", err)
		}
	}

	const key = "staging/v1/tus/source-url-integration/source.txt"
	const body = "private source"
	_, err = client.PutObject(ctx, &s3.PutObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(key),
		Body:   bytes.NewReader([]byte(body)),
	})
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = client.DeleteObject(context.Background(), &s3.DeleteObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(key),
		})
	})

	unsignedURL := strings.TrimRight(endpoint, "/") + "/" + bucket + "/" + key
	unsignedResponse, err := http.Get(unsignedURL) //nolint:noctx // integration probe
	require.NoError(t, err)
	_ = unsignedResponse.Body.Close()
	require.Equal(t, http.StatusForbidden, unsignedResponse.StatusCode)

	resolver, err := New(uploadconfig.StorageConfig{
		Driver: uploadconfig.StorageDriverS3,
		Public: uploadconfig.StoragePublicConfig{BaseURL: endpoint + "/" + bucket},
		S3: uploadconfig.StorageS3Config{
			Endpoint:       endpoint,
			SourceURLTTL:   time.Hour,
			Region:         region,
			Bucket:         bucket,
			AccessKey:      accessKey,
			SecretKey:      secretKey,
			ForcePathStyle: true,
		},
		Tus: uploadconfig.StorageTusConfig{StagingPrefix: "staging/v1/tus"},
	})
	require.NoError(t, err)

	signedURL, err := resolver.Resolve(ctx, key)
	require.NoError(t, err)
	signedResponse, err := http.Get(signedURL) //nolint:noctx // integration probe
	require.NoError(t, err)
	defer func() { _ = signedResponse.Body.Close() }()
	require.Equal(t, http.StatusOK, signedResponse.StatusCode)
	data, err := io.ReadAll(signedResponse.Body)
	require.NoError(t, err)
	require.Equal(t, body, string(data))
}

func sourceTestS3Client(endpoint, region, accessKey, secretKey string) *s3.Client {
	awsCfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		HTTPClient:  &http.Client{Timeout: 5 * time.Second},
	}
	return s3.NewFromConfig(awsCfg, func(options *s3.Options) {
		options.UsePathStyle = true
		options.BaseEndpoint = aws.String(endpoint)
		options.EndpointOptions.DisableHTTPS = strings.HasPrefix(endpoint, "http://")
	})
}

func sourceTestEnv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
