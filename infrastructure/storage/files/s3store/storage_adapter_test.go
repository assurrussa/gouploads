package s3store_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	s3store "github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
	s3storemocks "github.com/assurrussa/gouploads/infrastructure/storage/files/s3store/mocks"
)

func TestStorageAdapter_SaveTemp(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := s3storemocks.NewMockClient(ctrl)

	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	storage, err := s3store.NewStorageAdapter(mockClient, domain)
	require.NoError(t, err)

	body := bytes.Repeat([]byte("a"), 1024)
	reader := bytes.NewReader(body)

	mockClient.EXPECT().
		CreateMultipartUpload(gomock.Any(), gomock.AssignableToTypeOf(&s3.CreateMultipartUploadInput{})).
		DoAndReturn(func(
			_ context.Context,
			input *s3.CreateMultipartUploadInput,
			_ ...func(*s3.Options),
		) (*s3.CreateMultipartUploadOutput, error) {
			assert.Equal(t, "staging-bucket", aws.ToString(input.Bucket))
			assert.Equal(t, "staging/v1/tus/tmp/uploads/entity/folder/custom/1/example.txt", aws.ToString(input.Key))
			assert.Equal(t, "private,no-store", aws.ToString(input.CacheControl))
			assert.Equal(t, "text/plain", aws.ToString(input.ContentType))
			assert.Empty(t, input.ACL)
			return &s3.CreateMultipartUploadOutput{
				Bucket:   input.Bucket,
				Key:      input.Key,
				UploadId: aws.String("upload"),
			}, nil
		})

	mockClient.EXPECT().
		UploadPart(gomock.Any(), gomock.Any()).
		Return(&s3.UploadPartOutput{ETag: aws.String("etag")}, nil)

	mockClient.EXPECT().
		CompleteMultipartUpload(gomock.Any(), gomock.Any()).
		Return(&s3.CompleteMultipartUploadOutput{}, nil)

	temp, err := storage.SaveTemp(context.Background(), filestorage.SaveFileInput{
		Dir:      filestorage.FolderPrefixPathTemp.String() + "/entity/folder/custom/1",
		FileName: "Example.txt",
		MimeType: "text/plain",
		Reader:   reader,
	})
	require.NoError(t, err)
	assert.Equal(t, "staging/v1/tus/tmp/uploads/entity/folder/custom/1/example.txt", temp.RelativePath)
	assert.Equal(t, int64(len(body)), temp.Size)
	assert.Empty(t, temp.URL)
}

func TestStorageAdapter_SaveTempUploadError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := s3storemocks.NewMockClient(ctrl)
	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	storage, err := s3store.NewStorageAdapter(mockClient, domain)
	require.NoError(t, err)

	mockClient.EXPECT().
		CreateMultipartUpload(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("create error"))

	_, err = storage.SaveTemp(context.Background(), filestorage.SaveFileInput{
		Dir:      filestorage.FolderPrefixPathPersist.String() + "/entity/folder/custom/1",
		FileName: "Example.txt",
		Reader:   bytes.NewReader([]byte("data")),
		MimeType: "text/plain",
	})
	require.Error(t, err)
}

func TestStorageAdapter_SavePersist(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := s3storemocks.NewMockClient(ctrl)

	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	storage, err := s3store.NewStorageAdapter(mockClient, domain)
	require.NoError(t, err)

	body := bytes.Repeat([]byte("a"), 1024)
	reader := bytes.NewReader(body)

	mockClient.EXPECT().
		CreateMultipartUpload(gomock.Any(), gomock.AssignableToTypeOf(&s3.CreateMultipartUploadInput{})).
		DoAndReturn(func(
			_ context.Context,
			input *s3.CreateMultipartUploadInput,
			_ ...func(*s3.Options),
		) (*s3.CreateMultipartUploadOutput, error) {
			assert.Equal(t, "bucket", aws.ToString(input.Bucket))
			assert.Equal(t, "uploads/entity/folder/custom/1/example.txt", aws.ToString(input.Key))
			assert.Equal(t, "public,max-age=31536000,immutable", aws.ToString(input.CacheControl))
			assert.Equal(t, "text/plain", aws.ToString(input.ContentType))
			assert.Empty(t, input.ACL)
			return &s3.CreateMultipartUploadOutput{
				Bucket:   input.Bucket,
				Key:      input.Key,
				UploadId: aws.String("upload"),
			}, nil
		})

	mockClient.EXPECT().
		UploadPart(gomock.Any(), gomock.Any()).
		Return(&s3.UploadPartOutput{ETag: aws.String("etag")}, nil)

	mockClient.EXPECT().
		CompleteMultipartUpload(gomock.Any(), gomock.Any()).
		Return(&s3.CompleteMultipartUploadOutput{}, nil)

	temp, err := storage.SavePersist(context.Background(), filestorage.SaveFileInput{
		Dir:      filestorage.FolderPrefixPathPersist.String() + "/entity/folder/custom/1",
		FileName: "Example.txt",
		MimeType: "text/plain",
		Reader:   reader,
	})
	require.NoError(t, err)
	assert.Equal(t, "uploads/entity/folder/custom/1/example.txt", temp.RelativePath)
	assert.Equal(t, int64(len(body)), temp.Size)
	assert.Empty(t, temp.URL)
}

func TestStorageAdapter_SavePersistUploadError(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := s3storemocks.NewMockClient(ctrl)
	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	storage, err := s3store.NewStorageAdapter(mockClient, domain)
	require.NoError(t, err)

	mockClient.EXPECT().
		CreateMultipartUpload(gomock.Any(), gomock.Any()).
		Return(nil, errors.New("create error"))

	_, err = storage.SavePersist(context.Background(), filestorage.SaveFileInput{
		Dir:      filestorage.FolderPrefixPathTemp.String() + "/entity/folder/custom/1",
		FileName: "Example.txt",
		Reader:   bytes.NewReader([]byte("data")),
		MimeType: "text/plain",
	})
	require.Error(t, err)
}

func TestStorageAdapter_Commit(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := s3storemocks.NewMockClient(ctrl)
	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	storage, err := s3store.NewStorageAdapter(mockClient, domain)
	require.NoError(t, err)

	contentType := "text/plain"
	mockClient.EXPECT().
		HeadObject(gomock.Any(), gomock.AssignableToTypeOf(&s3.HeadObjectInput{})).
		DoAndReturn(func(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			assert.Equal(t, "staging-bucket", aws.ToString(input.Bucket))
			assert.Equal(t, "staging/v1/tus/session/source.txt", aws.ToString(input.Key))
			return &s3.HeadObjectOutput{ContentType: aws.String(contentType)}, nil
		})

	mockClient.EXPECT().
		CopyObject(gomock.Any(), gomock.AssignableToTypeOf(&s3.CopyObjectInput{})).
		DoAndReturn(func(_ context.Context, input *s3.CopyObjectInput, _ ...func(*s3.Options)) (*s3.CopyObjectOutput, error) {
			assert.Equal(t, "staging-bucket/staging/v1/tus/session/source.txt", aws.ToString(input.CopySource))
			assert.Equal(t, "bucket", aws.ToString(input.Bucket))
			assert.Equal(t, "media/v1/post/1/file/main.txt", aws.ToString(input.Key))
			assert.Empty(t, input.ACL)
			assert.Equal(t, "public,max-age=31536000,immutable", aws.ToString(input.CacheControl))
			assert.Equal(t, contentType, aws.ToString(input.ContentType))
			return &s3.CopyObjectOutput{}, nil
		})

	mockClient.EXPECT().
		DeleteObjects(gomock.Any(), gomock.AssignableToTypeOf(&s3.DeleteObjectsInput{}), gomock.Any()).
		DoAndReturn(func(_ context.Context, input *s3.DeleteObjectsInput, _ ...func(*s3.Options)) (*s3.DeleteObjectsOutput, error) {
			assert.Equal(t, "staging-bucket", aws.ToString(input.Bucket))
			return &s3.DeleteObjectsOutput{}, nil
		})

	contentLength := int64(10)
	mockClient.EXPECT().
		GetObject(gomock.Any(), gomock.AssignableToTypeOf(&s3.GetObjectInput{})).
		DoAndReturn(func(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			assert.Equal(t, "bucket", aws.ToString(input.Bucket))
			assert.Equal(t, "media/v1/post/1/file/main.txt", aws.ToString(input.Key))
			reader := io.NopCloser(bytes.NewReader([]byte("a")))
			return &s3.GetObjectOutput{
				Body:          reader,
				ContentRange:  aws.String("bytes 0-0/10"),
				ContentLength: &contentLength,
				ContentType:   aws.String(contentType),
			}, nil
		})

	stored, err := storage.Commit(context.Background(), filestorage.CommitInput{
		Path:     "staging/v1/tus/session/source.txt",
		DestDir:  "media/v1/post/1/file",
		FileName: "main.txt",
	})
	require.NoError(t, err)
	assert.Equal(t, "media/v1/post/1/file/main.txt", stored.RelativePath)
	assert.Equal(t, int64(10), stored.Size)
	assert.Equal(t, contentType, stored.MimeType)
	assert.Empty(t, stored.URL)
}

func TestStorageAdapter_Open(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := s3storemocks.NewMockClient(ctrl)
	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	storage, err := s3store.NewStorageAdapter(mockClient, domain)
	require.NoError(t, err)

	mockClient.EXPECT().
		GetObject(gomock.Any(), gomock.AssignableToTypeOf(&s3.GetObjectInput{})).
		DoAndReturn(func(_ context.Context, input *s3.GetObjectInput, _ ...func(*s3.Options)) (*s3.GetObjectOutput, error) {
			assert.Equal(t, "tmp/uploads/example.txt", aws.ToString(input.Key))
			return &s3.GetObjectOutput{Body: io.NopCloser(bytes.NewReader([]byte("data")))}, nil
		})

	reader, err := storage.Open(context.Background(), "tmp/uploads/example.txt")
	require.NoError(t, err)
	defer reader.Close()

	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	assert.Equal(t, []byte("data"), data)
}

func TestStorageAdapter_Delete(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := s3storemocks.NewMockClient(ctrl)
	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	storage, err := s3store.NewStorageAdapter(mockClient, domain)
	require.NoError(t, err)

	mockClient.EXPECT().
		DeleteObjects(gomock.Any(), gomock.AssignableToTypeOf(&s3.DeleteObjectsInput{}), gomock.Any()).
		Return(&s3.DeleteObjectsOutput{}, nil)

	require.NoError(t, storage.Delete(context.Background(), "tmp/uploads/example.txt"))
}

func TestStorageAdapter_DeleteReportsPerObjectErrors(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := s3storemocks.NewMockClient(ctrl)
	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	storage, err := s3store.NewStorageAdapter(mockClient, domain)
	require.NoError(t, err)

	mockClient.EXPECT().
		DeleteObjects(gomock.Any(), gomock.AssignableToTypeOf(&s3.DeleteObjectsInput{}), gomock.Any()).
		Return(&s3.DeleteObjectsOutput{Errors: []types.Error{{
			Key:     aws.String("media/v1/post/1/file/main.png"),
			Code:    aws.String("AccessDenied"),
			Message: aws.String("denied"),
		}}}, nil)

	err = storage.Delete(context.Background(), "media/v1/post/1/file/main.png")
	require.ErrorContains(t, err, "provider rejected 1 object")
	require.ErrorContains(t, err, "AccessDenied")
}

func TestStorageAdapter_Exists(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	mockClient := s3storemocks.NewMockClient(ctrl)
	domain := s3store.NewDomainHost("https://storage.example.com", "bucket", "staging-bucket", "staging/v1/tus")
	storage, err := s3store.NewStorageAdapter(mockClient, domain)
	require.NoError(t, err)

	// Test case 1: File exists
	mockClient.EXPECT().
		HeadObject(gomock.Any(), gomock.Any()).
		Return(&s3.HeadObjectOutput{}, nil)

	exist, err := storage.Exists(context.Background(), filestorage.ExistFileInput{
		Path: "test-dir/test-file.txt",
	})
	require.NoError(t, err)
	assert.True(t, exist.Exist)

	// Test case 2: File does not exist
	mockClient.EXPECT().
		HeadObject(gomock.Any(), gomock.Any()).
		Return(nil, &types.NotFound{})

	exist, err = storage.Exists(context.Background(), filestorage.ExistFileInput{
		Path: "test-dir/non-existent-file.txt",
	})
	require.NoError(t, err)
	assert.False(t, exist.Exist)
}
