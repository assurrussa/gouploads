package tusupload

import (
	"net/http"
	"path/filepath"

	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/aws/retry"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
)

func BuildDurableStore(storageCfg config.StorageConfig, database pgsql.Client) (Store, error) {
	if storageCfg.Driver != config.StorageDriverS3 {
		return NewFileStore(filepath.Join(storageCfg.Local.Root, "tmp", "tus"))
	}

	return buildS3Store(storageCfg, database)
}

func buildS3Store(storageCfg config.StorageConfig, database pgsql.Client) (Store, error) {
	var err error
	storageCfg, err = config.NormalizeStorageConfig(storageCfg)
	if err != nil {
		return nil, err
	}
	repo, err := newPostgresSessionRepository(database)
	if err != nil {
		return nil, err
	}

	s3cfg := storageCfg.S3
	httpClient := &http.Client{}
	if s3cfg.Timeout > 0 {
		httpClient.Timeout = s3cfg.Timeout
	}

	awsCfg := aws.Config{
		Region:      s3cfg.Region,
		Credentials: credentials.NewStaticCredentialsProvider(s3cfg.AccessKey, s3cfg.SecretKey, s3cfg.SessionToken),
		HTTPClient:  httpClient,
		Retryer: func() aws.Retryer {
			return retry.NewStandard(func(options *retry.StandardOptions) {
				options.MaxAttempts = s3cfg.MaxRetries + 1
			})
		},
	}

	s3Client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		o.UsePathStyle = s3cfg.ForcePathStyle
		if s3cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(s3cfg.Endpoint)
		}
	})

	domain := s3store.NewDomainHost(
		storageCfg.Public.BaseURL,
		s3cfg.Bucket,
		s3cfg.StagingBucket,
		storageCfg.Tus.StagingPrefix,
	)

	partSize := int64(storageCfg.Tus.PartSize.Value())
	store, err := NewS3Store(s3Client, domain, repo, S3StoreConfig{
		Prefix:   storageCfg.Tus.StagingPrefix,
		PartSize: partSize,
		TTL:      storageCfg.Tus.SessionTTL,
		LeaseTTL: storageCfg.Tus.LeaseTTL,
	})
	if err != nil {
		return nil, err
	}

	return store, nil
}
