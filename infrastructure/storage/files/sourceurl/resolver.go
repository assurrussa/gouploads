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
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	uploadconfig "github.com/assurrussa/gouploads/config"
)

const (
	DefaultS3URLTTL = 6 * time.Hour
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
	presigner  *awss3.PresignClient
	bucket     string
	sourceHost string
	ttl        time.Duration
}

// New builds the resolver for the configured storage driver. Local storage
// keeps its existing source URL flow; S3 creates a SigV4 presigned GET URL.
func New(cfg uploadconfig.StorageConfig) (Resolver, error) {
	if cfg.Driver != uploadconfig.StorageDriverS3 {
		return passthroughResolver{}, nil
	}

	storageCfg := cfg.S3
	ttl := storageCfg.SourceURLTTL
	if ttl == 0 {
		ttl = DefaultS3URLTTL
	}
	if ttl < time.Minute || ttl > MaxS3URLTTL {
		return nil, fmt.Errorf("s3 source URL TTL must be between 1m and %s", MaxS3URLTTL)
	}
	if strings.TrimSpace(storageCfg.Bucket) == "" {
		return nil, errors.New("s3 source URL bucket is required")
	}

	sourceHost, err := normalizeSourceHost(storageCfg.SourceHost)
	if err != nil {
		return nil, err
	}

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
	}
	client := awss3.NewFromConfig(awsCfg, func(options *awss3.Options) {
		options.UsePathStyle = storageCfg.ForcePathStyle
		if storageCfg.Endpoint != "" {
			options.BaseEndpoint = aws.String(storageCfg.Endpoint)
		}
		options.EndpointOptions.DisableHTTPS = storageCfg.DisableSSL
	})

	return &s3Resolver{
		presigner:  awss3.NewPresignClient(client),
		bucket:     strings.Trim(storageCfg.Bucket, "/"),
		sourceHost: sourceHost,
		ttl:        ttl,
	}, nil
}

func (r *s3Resolver) Resolve(ctx context.Context, source string) (string, error) {
	key, err := objectKey(source, r.bucket)
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

	return rewriteURLHost(request.URL, r.sourceHost)
}

func objectKey(source, bucket string) (string, error) {
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
	return key, nil
}

func normalizeSourceHost(raw string) (string, error) {
	value := strings.TrimSpace(raw)
	if value == "" {
		return "", nil
	}

	parsed, err := url.Parse(value)
	if err != nil {
		return "", fmt.Errorf("parse s3 source host: %w", err)
	}
	if parsed.Scheme == "" || parsed.Host == "" {
		return "", errors.New("s3 source host requires scheme and host")
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", errors.New("s3 source host must not contain userinfo, path, query, or fragment")
	}

	return parsed.Scheme + "://" + parsed.Host, nil
}

func rewriteURLHost(rawURL, sourceHost string) (string, error) {
	if sourceHost == "" {
		return rawURL, nil
	}

	signed, err := url.Parse(rawURL)
	if err != nil {
		return "", fmt.Errorf("parse presigned URL: %w", err)
	}
	target, err := url.Parse(sourceHost)
	if err != nil {
		return "", fmt.Errorf("parse source host: %w", err)
	}

	signed.Scheme = target.Scheme
	signed.Host = target.Host
	return signed.String(), nil
}
