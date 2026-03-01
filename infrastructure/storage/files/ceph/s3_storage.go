package ceph

import (
	"context"
	"fmt"
	"io"
	"net/url"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
)

type s3Storage struct {
	host     string
	domain   DomainHost
	s3Client S3Client
}

func NewS3Storage(client S3Client, domain DomainHost) S3Storage {
	return &s3Storage{
		host:     domain.String(),
		domain:   domain,
		s3Client: client,
	}
}

// UploadFile загрузка файла в ceph.
func (u *s3Storage) UploadFile(
	ctx context.Context,
	buffer io.Reader,
	bucketKey string,
	contentType string,
) (string, error) {
	err := u.s3Client.Upload(ctx, s3sdk.CreateMultipartUploadInput{
		ACL:         types.ObjectCannedACL(u.domain.ACL()),
		Bucket:      aws.String(u.domain.Bucket()),
		Key:         aws.String(bucketKey),
		ContentType: aws.String(contentType),
	}, buffer)
	if err != nil {
		return "", fmt.Errorf("can't upload \"%s\" to s3: %w", bucketKey, err)
	}

	return u.getExternalLink(bucketKey), nil
}

// CheckBucketExists проверяет на существование bucket.
func (u *s3Storage) CheckBucketExists(ctx context.Context) error {
	err := u.s3Client.CheckBucketExists(ctx, u.domain.Bucket())
	if err != nil {
		return fmt.Errorf("check bucket: %w", err)
	}

	return nil
}

// ListBucket Получает список файлов в бакете.
func (u *s3Storage) ListBucket(ctx context.Context) (*s3sdk.ListObjectsV2Output, error) {
	output, err := u.s3Client.ListObjectsV2(ctx, u.domain.Bucket())
	if err != nil {
		return nil, fmt.Errorf("ListObjectsV2 bucket: %w", err)
	}

	return output, nil
}

// CheckBucketExists проверяет на существование bucket.
func (u *s3Storage) getExternalLink(bucketKey string) string {
	link, _ := url.JoinPath(u.domain.Host(), u.domain.Bucket(), bucketKey)
	return link
}
