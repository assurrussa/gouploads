package host

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/ceph"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/local"
)

// NewStorage builds the stable host-facing file storage selected by cfg.
// S3 configuration is compatible with AWS S3 and path-style MinIO endpoints.
func NewStorage(cfg StorageConfig) (Storage, error) {
	switch cfg.Driver {
	case StorageDriverS3:
		httpClient := &http.Client{}
		if cfg.S3.Timeout > 0 {
			httpClient.Timeout = cfg.S3.Timeout
		}
		awsConfig := aws.Config{
			Region: cfg.S3.Region,
			Credentials: credentials.NewStaticCredentialsProvider(
				cfg.S3.AccessKey, cfg.S3.SecretKey, cfg.S3.SessionToken,
			),
			HTTPClient: httpClient,
		}
		client := awss3.NewFromConfig(awsConfig, func(options *awss3.Options) {
			options.UsePathStyle = cfg.S3.ForcePathStyle
			if cfg.S3.Endpoint != "" {
				options.BaseEndpoint = aws.String(cfg.S3.Endpoint)
			}
			options.EndpointOptions.DisableHTTPS = cfg.S3.DisableSSL
		})
		host := strings.TrimSpace(cfg.S3.Host)
		if host == "" {
			host = strings.TrimSpace(cfg.S3.Endpoint)
		}
		domain := ceph.NewDomainHost(
			host, cfg.S3.Bucket, cfg.S3.ACL, cfg.S3.TransformHost,
			cfg.S3.DisableSSL, cfg.S3.ForcePathStyle,
		)
		storage, err := ceph.NewStorageAdapter(client, domain)
		if err != nil {
			return nil, fmt.Errorf("create s3 storage: %w", err)
		}
		return storage, nil
	case "", StorageDriverLocal:
		storage, err := local.New(cfg.Local.Root, cfg.Local.BaseURL)
		if err != nil {
			return nil, fmt.Errorf("create local storage: %w", err)
		}
		return storage, nil
	default:
		return nil, fmt.Errorf("unsupported storage driver %q", cfg.Driver)
	}
}
