package tusupload

import (
	"errors"
	"net/http"
	"path/filepath"

	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	awss3 "github.com/aws/aws-sdk-go-v2/service/s3"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/infrastructure/storage/files/ceph"
)

func BuildStore(storageCfg config.StorageConfig, _ redisClient) (Store, error) {
	if storageCfg.Driver != config.StorageDriverS3 {
		return NewFileStore(filepath.Join(storageCfg.Local.Root, "tmp", "tus"))
	}

	return nil, errors.New("tus store: S3 requires BuildDurableStore with PostgreSQL")
}

func BuildDurableStore(storageCfg config.StorageConfig, database pgsql.Client) (Store, error) {
	if storageCfg.Driver != config.StorageDriverS3 {
		return NewFileStore(filepath.Join(storageCfg.Local.Root, "tmp", "tus"))
	}

	return buildS3Store(storageCfg, database)
}

func buildS3Store(storageCfg config.StorageConfig, database pgsql.Client) (Store, error) {
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
	}

	s3Client := awss3.NewFromConfig(awsCfg, func(o *awss3.Options) {
		o.UsePathStyle = s3cfg.ForcePathStyle
		if s3cfg.Endpoint != "" {
			o.BaseEndpoint = aws.String(s3cfg.Endpoint)
		}
		o.EndpointOptions.DisableHTTPS = s3cfg.DisableSSL
	})

	host := s3cfg.Host
	if host == "" {
		host = s3cfg.Endpoint
	}

	domain := ceph.NewDomainHost(
		host,
		s3cfg.Bucket,
		s3cfg.ACL,
		s3cfg.TransformHost,
		s3cfg.DisableSSL,
		s3cfg.ForcePathStyle,
	)

	partSize := int64(storageCfg.Tus.PartSize.Value())
	store, err := NewS3Store(s3Client, domain, repo, S3StoreConfig{
		Prefix:   storageCfg.Tus.QuarantinePrefix,
		PartSize: partSize,
		TTL:      storageCfg.Tus.SessionTTL,
		LeaseTTL: storageCfg.Tus.LeaseTTL,
	})
	if err != nil {
		return nil, err
	}

	return store, nil
}
