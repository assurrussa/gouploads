package sourceurl

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/assurrussa/goshared/pkg/filesanitize"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	uploadconfig "github.com/assurrussa/gouploads/config"
)

const (
	DefaultS3URLTTL = 15 * time.Minute
	MaxS3URLTTL     = 7 * 24 * time.Hour
)

// Resolver turns a durable source reference into an HTTP URL immediately
// before a media processing request is dispatched.
type Resolver interface {
	Resolve(ctx context.Context, source string) (string, error)
}

type passthroughResolver struct{}

func (passthroughResolver) Resolve(_ context.Context, source string) (string, error) {
	return source, nil
}

type s3Resolver struct {
	presigner     *awss3.PresignClient
	bucket        string
	stagingPrefix string
	ttl           time.Duration
}

// New builds the resolver for the configured storage driver. Local storage
// keeps its existing source URL flow; S3 creates a SigV4 presigned GET URL.
func New(cfg uploadconfig.StorageConfig) (Resolver, error) {
	if cfg.Driver != uploadconfig.StorageDriverS3 {
		return passthroughResolver{}, nil
	}

	var err error
	cfg, err = uploadconfig.NormalizeStorageConfig(cfg)
	if err != nil {
		return nil, fmt.Errorf("validate s3 storage config: %w", err)
	}
	storageCfg := cfg.S3
	ttl := storageCfg.SourceURLTTL
	bucket := strings.TrimSpace(storageCfg.StagingBucket)

	httpClient := &http.Client{}
	if storageCfg.Timeout > 0 {
		httpClient.Timeout = storageCfg.Timeout
	}
	awsCfg := aws.Config{
		Region: storageCfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(
			storageCfg.AccessKey,
			storageCfg.SecretKey,
			storageCfg.SessionToken,
		),
		HTTPClient: httpClient,
		Retryer: func() aws.Retryer {
			return retry.NewStandard(func(options *retry.StandardOptions) {
				options.MaxAttempts = storageCfg.MaxRetries + 1
			})
		},
	}
	client := awss3.NewFromConfig(awsCfg, func(options *awss3.Options) {
		options.UsePathStyle = storageCfg.ForcePathStyle
		if storageCfg.Endpoint != "" {
			options.BaseEndpoint = aws.String(storageCfg.Endpoint)
		}
	})

	return &s3Resolver{
		presigner:     awss3.NewPresignClient(client),
		bucket:        strings.Trim(bucket, "/"),
		stagingPrefix: strings.Trim(cfg.Tus.StagingPrefix, "/"),
		ttl:           ttl,
	}, nil
}

func (r *s3Resolver) Resolve(ctx context.Context, source string) (string, error) {
	key, err := objectKey(source, r.bucket, r.stagingPrefix)
	if err != nil {
		return "", fmt.Errorf("resolve s3 source key: %w", err)
	}

	request, err := r.presigner.PresignGetObject(
		ctx,
		&awss3.GetObjectInput{
			Bucket: aws.String(r.bucket),
			Key:    aws.String(key),
		},
		func(options *awss3.PresignOptions) {
			options.Expires = r.ttl
		},
	)
	if err != nil {
		return "", fmt.Errorf("presign s3 GetObject: %w", err)
	}

	return request.URL, nil
}

func objectKey(source, bucket, stagingPrefix string) (string, error) {
	raw := strings.TrimSpace(source)
	if raw == "" {
		return "", errors.New("source is required")
	}

	key := raw
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", fmt.Errorf("parse source URL: %w", err)
	}
	if parsed.IsAbs() || parsed.Host != "" {
		key = parsed.Path
	}

	key = strings.TrimLeft(key, "/")
	bucketPrefix := strings.Trim(bucket, "/") + "/"
	key = strings.TrimPrefix(key, bucketPrefix)

	key, err = filesanitize.EnsureRelativePath(key)
	if err != nil {
		return "", err
	}
	stagingPrefix = strings.Trim(stagingPrefix, "/")
	if stagingPrefix == "" || (key != stagingPrefix && !strings.HasPrefix(key, stagingPrefix+"/")) {
		return "", errors.New("source key is outside the configured staging prefix")
	}
	return key, nil
}
