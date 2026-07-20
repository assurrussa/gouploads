package s3store

import (
	"context"
	"io"

	"github.com/aws/aws-sdk-go-v2/service/s3"
)

//go:generate toolsmocks

type Client interface {
	MultiPartUploader
	HeadBucket(
		ctx context.Context,
		input *s3.HeadBucketInput,
		optFns ...func(*s3.Options),
	) (*s3.HeadBucketOutput, error)
	ListObjectsV2(
		ctx context.Context,
		input *s3.ListObjectsV2Input,
		optFns ...func(*s3.Options),
	) (*s3.ListObjectsV2Output, error)
	DeleteObjects(
		ctx context.Context,
		input *s3.DeleteObjectsInput,
		optFns ...func(*s3.Options),
	) (*s3.DeleteObjectsOutput, error)
	CopyObject(
		ctx context.Context,
		input *s3.CopyObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.CopyObjectOutput, error)
	GetObject(
		ctx context.Context,
		input *s3.GetObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.GetObjectOutput, error)
	HeadObject(
		ctx context.Context,
		input *s3.HeadObjectInput,
		optFns ...func(*s3.Options),
	) (*s3.HeadObjectOutput, error)
}

type MultiPartUploader interface {
	CreateMultipartUpload(
		ctx context.Context,
		input *s3.CreateMultipartUploadInput,
		optFns ...func(*s3.Options),
	) (*s3.CreateMultipartUploadOutput, error)
	AbortMultipartUpload(
		ctx context.Context,
		input *s3.AbortMultipartUploadInput,
		optFns ...func(*s3.Options),
	) (*s3.AbortMultipartUploadOutput, error)
	UploadPart(
		ctx context.Context,
		input *s3.UploadPartInput,
		optFns ...func(*s3.Options),
	) (*s3.UploadPartOutput, error)
	CompleteMultipartUpload(
		ctx context.Context,
		input *s3.CompleteMultipartUploadInput,
		optFns ...func(*s3.Options),
	) (*s3.CompleteMultipartUploadOutput, error)
}

// S3Storage интерфейс для загрузки в файловое хранилище.
type S3Storage interface {
	// UploadFile загрузка файла в хранилище
	UploadFile(ctx context.Context, buffer io.Reader, key string, contentType string) (string, error)
	CheckBucketExists(ctx context.Context) error
	ListBucket(ctx context.Context) (*s3.ListObjectsV2Output, error)
}

type S3Client interface {
	// Upload загружает файл в S3-совместимое хранилище.
	Upload(
		ctx context.Context,
		input s3.CreateMultipartUploadInput,
		reader io.Reader,
		optFns ...func(options *s3.Options),
	) error
	// CheckBucketExists проверяет на существование bucket
	CheckBucketExists(ctx context.Context, bucket string) error
	// ListObjectsV2 Получает список файлов в бакете
	ListObjectsV2(ctx context.Context, bucket string) (*s3.ListObjectsV2Output, error)
}
