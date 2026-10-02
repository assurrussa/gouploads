package s3store_test

import (
	"context"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	s3store "github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
	s3storemocks "github.com/assurrussa/gouploads/infrastructure/storage/files/s3store/mocks"
)

func TestStorageAdapter_CommitSameObjectPreservesSource(t *testing.T) {
	client := s3storemocks.NewMockClient(gomock.NewController(t))
	storage, err := s3store.NewStorageAdapter(client,
		s3store.NewDomainHost("", "files", "staging", "staging/v1/tus"))
	require.NoError(t, err)

	client.EXPECT().HeadObject(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, input *s3.HeadObjectInput, _ ...func(*s3.Options)) (*s3.HeadObjectOutput, error) {
			require.Equal(t, "files", aws.ToString(input.Bucket))
			require.Equal(t, "media/v1/post/1/file/main.pdf", aws.ToString(input.Key))
			return &s3.HeadObjectOutput{ContentLength: aws.Int64(12), ContentType: aws.String("application/pdf")}, nil
		})
	// No copy or delete is permitted when source and destination are identical.
	stored, err := storage.Commit(context.Background(), filestorage.CommitInput{
		Path: "media/v1/post/1/file/main.pdf", DestDir: "media/v1/post/1/file", FileName: "main.pdf",
	})
	require.NoError(t, err)
	require.Equal(t, "media/v1/post/1/file/main.pdf", stored.RelativePath)
	require.Equal(t, int64(12), stored.Size)
	require.Equal(t, "application/pdf", stored.MimeType)
}

func TestStorageAdapter_CommitSameObjectPreservesHeadErrors(t *testing.T) {
	for _, cause := range []error{&types.NotFound{}, context.Canceled} {
		t.Run(cause.Error(), func(t *testing.T) {
			client := s3storemocks.NewMockClient(gomock.NewController(t))
			storage, err := s3store.NewStorageAdapter(client,
				s3store.NewDomainHost("", "files", "staging", "staging/v1/tus"))
			require.NoError(t, err)
			client.EXPECT().HeadObject(gomock.Any(), gomock.Any()).Return(nil, cause)
			stored, err := storage.Commit(t.Context(), filestorage.CommitInput{
				Path: "media/v1/post/1/file/main.pdf", DestDir: "media/v1/post/1/file", FileName: "main.pdf",
			})
			require.ErrorIs(t, err, cause)
			require.Empty(t, stored.RelativePath)
		})
	}
}
