package s3store

import (
	"context"
	"fmt"
	"io"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
)

type s3Client struct {
	Client Client
}

func News3Client(client Client) S3Client {
	return &s3Client{
		Client: client,
	}
}

// Upload загрузка файла в s3store.
func (s *s3Client) Upload(
	ctx context.Context,
	input s3sdk.CreateMultipartUploadInput,
	reader io.Reader,
	optFns ...func(*s3sdk.Options),
) error {
	return Upload(ctx, s.Client, &input, reader, optFns...)
}

// CheckBucketExists проверяет на существование bucket.
func (s *s3Client) CheckBucketExists(ctx context.Context, bucket string) error {
	_, err := s.Client.HeadBucket(ctx, &s3sdk.HeadBucketInput{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return fmt.Errorf("can't check bucket exists [%s]: %w", bucket, err)
	}

	return nil
}

// ListObjectsV2 Получает список файлов в бакете.
func (s *s3Client) ListObjectsV2(ctx context.Context, bucket string) (*s3sdk.ListObjectsV2Output, error) {
	out, err := s.Client.ListObjectsV2(ctx, &s3sdk.ListObjectsV2Input{
		Bucket: aws.String(bucket),
	})
	if err != nil {
		return nil, fmt.Errorf("can't ListObjectsV2 bucket [%s]: %w", bucket, err)
	}

	return out, nil
}
