package ceph_test

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	ceph2 "github.com/assurrussa/gouploads/infrastructure/storage/files/ceph"
	cephmocks "github.com/assurrussa/gouploads/infrastructure/storage/files/ceph/mocks"
)

func TestS3Storage_Upload(t *testing.T) {
	tests := []struct {
		name    string
		bucket  string
		err     error
		prepare func(ctx context.Context, client *cephmocks.MockS3Client, file io.Reader, err error)
	}{
		{
			name:   "base",
			bucket: "test_bucket",
			err:    nil,
			prepare: func(ctx context.Context, client *cephmocks.MockS3Client, file io.Reader, err error) {
				client.EXPECT().Upload(ctx, gomock.Any(), file).Return(err)
			},
		},
		{
			name:   "base",
			bucket: "test_bucket",
			err:    errors.New("error test"),
			prepare: func(ctx context.Context, client *cephmocks.MockS3Client, file io.Reader, err error) {
				client.EXPECT().Upload(ctx, gomock.Any(), file).Return(err)
			},
		},
	}

	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctx := context.Background()
			mockCtl := gomock.NewController(t)
			defer mockCtl.Finish()
			mockClient := cephmocks.NewMockS3Client(mockCtl)
			file, err := os.Open("test_data/GeoIP2-City-Test.mmdb")
			tt.prepare(ctx, mockClient, file, tt.err)
			defer func() { _ = file.Close() }()
			require.NoError(t, err)
			bucketKey := "keyname.mmdb"
			domain := ceph2.NewDomainHost("https://test.app/", tt.bucket, "public-read", true, false, false)
			storage := ceph2.NewS3Storage(mockClient, domain)
			bucketkey, err := storage.UploadFile(ctx, file, bucketKey, "text/octet-stream")
			if tt.err != nil {
				require.Empty(t, bucketkey)
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.err)
			} else {
				require.NoError(t, err)
				assert.Equal(t, "https://test.app/"+tt.bucket+"/"+bucketKey, bucketkey)
			}
		})
	}
}

func TestS3Storage_CheckBucketExists(t *testing.T) {
	tests := []struct {
		name   string
		host   string
		bucket string
		err    error
	}{
		{
			name:   "1",
			host:   "https://test.app/",
			bucket: "test_bucket",
			err:    nil,
		},
		{
			name:   "2",
			host:   "http://test.app/",
			bucket: "test_bucket",
			err:    errors.New("foo"),
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			defer ctrl.Finish()
			m := cephmocks.NewMockClient(ctrl)
			m.EXPECT().
				HeadBucket(gomock.Any(), &s3sdk.HeadBucketInput{Bucket: aws.String(tt.bucket)}).
				Return(&s3sdk.HeadBucketOutput{}, tt.err)

			domain := ceph2.NewDomainHost(tt.host, tt.bucket, "public-read", false, false, false)
			client := ceph2.NewS3Storage(ceph2.News3Client(m), domain)
			err := client.CheckBucketExists(context.Background())
			if tt.err != nil {
				require.Error(t, err)
				assert.ErrorIs(t, err, tt.err)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}
