//go:build integration

//nolint:testpackage // integration tests exercise private session repository and fencing internals.
package tusupload

import (
	"context"
	"errors"
	"net/http"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"

	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
)

func TestIntegration_S3Store_IntegrationFlow(t *testing.T) {
	ctx := context.Background()

	endpoint := getenv("TEST_S3_ENDPOINT", "http://localhost:9090")
	accessKey := getenv("TEST_S3_ACCESS_KEY", "minioadmin")
	secretKey := getenv("TEST_S3_SECRET_KEY", "minioadmin")
	bucket := getenv("TEST_S3_BUCKET", "tus-integration-tests")
	region := getenv("TEST_S3_REGION", "us-east-1")

	httpClient := &http.Client{Timeout: 5 * time.Second}
	awsCfg := aws.Config{
		Region:      region,
		Credentials: credentials.NewStaticCredentialsProvider(accessKey, secretKey, ""),
		HTTPClient:  httpClient,
	}

	client := s3.NewFromConfig(awsCfg, func(o *s3.Options) {
		o.UsePathStyle = true
		if endpoint != "" {
			o.BaseEndpoint = aws.String(endpoint)
		}
		o.EndpointOptions.DisableHTTPS = strings.HasPrefix(endpoint, "http://")
	})

	pingCtx, cancel := context.WithTimeout(ctx, 3*time.Second)
	defer cancel()
	_, err := client.ListBuckets(pingCtx, &s3.ListBucketsInput{})
	require.NoError(t, err, "mandatory S3 integration dependency is unavailable")

	if _, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)}); err != nil {
		var owned *s3types.BucketAlreadyOwnedByYou
		var exists *s3types.BucketAlreadyExists
		if !errors.As(err, &owned) && !errors.As(err, &exists) {
			t.Fatalf("create bucket: %v", err)
		}
	}

	database, _, databaseCleanup := testshelpers.PrepareDB(ctx, t, "tusintegration")
	defer databaseCleanup(ctx)
	repo, err := newPostgresSessionRepository(database)
	require.NoError(t, err)

	domain := s3store.NewDomainHost(endpoint, bucket, "", "staging/v1/tus")
	storeA, err := NewS3Store(client, domain, repo, S3StoreConfig{
		Prefix:   defaultS3StorePrefix,
		PartSize: s3store.MinPartSize,
	})
	require.NoError(t, err)
	storeB, err := NewS3Store(client, domain, repo, S3StoreConfig{
		Prefix:   defaultS3StorePrefix,
		PartSize: s3store.MinPartSize,
	})
	require.NoError(t, err)

	payload := []byte("hello tus integration")
	session, err := storeA.Create(ctx, CreateRequest{
		UploadLength: int64(len(payload)),
		OriginalName: "hello.txt",
		FileName:     "hello.txt",
		Metadata: map[string]string{
			"entity_type": "exercise",
			"entity_id":   "99",
		},
	})
	require.NoError(t, err)

	_, err = storeB.Append(ctx, session.ID, 0, payload, "text/plain")
	require.NoError(t, err)

	complete, err := storeA.Complete(ctx, session.ID)
	require.NoError(t, err)
	repeated, err := storeB.Complete(ctx, session.ID)
	require.NoError(t, err)
	require.Equal(t, complete.FinalizationKey, repeated.FinalizationKey)

	_, err = client.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(complete.RelativePath),
	})
	require.NoError(t, err)

	_, _ = client.DeleteObject(ctx, &s3.DeleteObjectInput{
		Bucket: aws.String(bucket),
		Key:    aws.String(complete.RelativePath),
	})
}

func getenv(key, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(key)); value != "" {
		return value
	}
	return fallback
}
