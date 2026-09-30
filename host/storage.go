package host

import (
	"fmt"
	"net/http"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	uploadconfig "github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/local"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
)

func NewStorage(cfg StorageConfig) (Storage, error) {
	switch cfg.Driver {
	case StorageDriverS3:
		var err error
		cfg, err = uploadconfig.NormalizeStorageConfig(cfg)
		if err != nil {
			return nil, fmt.Errorf("validate s3 storage config: %w", err)
		}
		httpClient := &http.Client{Timeout: cfg.S3.Timeout}
		awsConfig := aws.Config{
			Region: cfg.S3.Region,
			Credentials: credentials.NewStaticCredentialsProvider(cfg.S3.AccessKey,
				cfg.S3.SecretKey,
				cfg.S3.SessionToken),
			HTTPClient: httpClient,
			Retryer: func() aws.Retryer {
				return retry.NewStandard(func(options *retry.StandardOptions) { options.MaxAttempts = cfg.S3.MaxRetries + 1 })
			},
		}
		client := awss3.NewFromConfig(awsConfig, func(options *awss3.Options) {
			options.UsePathStyle = cfg.S3.ForcePathStyle
			if cfg.S3.Endpoint != "" {
				options.BaseEndpoint = aws.String(cfg.S3.Endpoint)
			}
		})
		domain := s3store.NewDomainHost(cfg.Public.BaseURL, cfg.S3.Bucket, cfg.S3.StagingBucket, cfg.Tus.StagingPrefix)
		storage, err := s3store.NewStorageAdapter(client, domain)
		if err != nil {
			return nil, fmt.Errorf("create s3 storage: %w", err)
		}
		return storage, nil
	case "", StorageDriverLocal:
		storage, err := local.NewWithPublicPrefix(cfg.Local.Root, cfg.Local.BaseURL, cfg.Public.Prefix)
		if err != nil {
			return nil, fmt.Errorf("create local storage: %w", err)
		}
		return storage, nil
	default:
		return nil, fmt.Errorf("unsupported storage driver %q", cfg.Driver)
	}
}
