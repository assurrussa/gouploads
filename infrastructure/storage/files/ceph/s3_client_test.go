package ceph_test

import (
	"context"
	"errors"
	"io"
	"os"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	s3sdk "github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	ceph2 "github.com/assurrussa/gouploads/infrastructure/storage/files/ceph"
	cephmocks "github.com/assurrussa/gouploads/infrastructure/storage/files/ceph/mocks"
)

func Test_s3Client_CheckBucketExists(t *testing.T) {
	ctx := context.Background()
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	s3Client := cephmocks.NewMockClient(mockCtl)

	type fields struct {
		Client ceph2.Client
	}
	type args struct {
		bucket string
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		prepare func()
		wantErr bool
	}{
		{
			name:   "correct",
			fields: fields{Client: s3Client},
			args: args{
				bucket: "test",
			},
			prepare: func() {
				s3Client.EXPECT().HeadBucket(gomock.Any(), &s3sdk.HeadBucketInput{
					Bucket: aws.String("test"),
				}).Return(nil, nil)
			},
			wantErr: false,
		},
		{
			name:   "error",
			fields: fields{Client: s3Client},
			args: args{
				bucket: "test",
			},
			prepare: func() {
				s3Client.EXPECT().HeadBucket(gomock.Any(), &s3sdk.HeadBucketInput{
					Bucket: aws.String("test"),
				}).Return(nil, errors.New("error 1"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			tt.prepare()
			s := ceph2.News3Client(tt.fields.Client)
			if err := s.CheckBucketExists(ctx, tt.args.bucket); (err != nil) != tt.wantErr {
				t.Errorf("CheckBucketExists() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}

func Test_s3Client_Upload(t *testing.T) {
	ctx := context.Background()
	mockCtl := gomock.NewController(t)
	defer mockCtl.Finish()

	s3Client := cephmocks.NewMockClient(mockCtl)
	file, err := os.Open("test_data/GeoIP2-City-Test.mmdb")
	require.NoError(t, err)

	type fields struct {
		Client ceph2.Client
	}
	type args struct {
		input  s3sdk.CreateMultipartUploadInput
		reader io.Reader
	}
	tests := []struct {
		name    string
		fields  fields
		args    args
		prepare func()
		wantErr bool
	}{
		{
			name:   "correct",
			fields: fields{Client: s3Client},
			args: args{
				input: s3sdk.CreateMultipartUploadInput{
					ACL:    types.ObjectCannedACL("testACL"),
					Bucket: aws.String("testBucket"),
					Key:    aws.String("testKey"),
				},
				reader: file,
			},
			prepare: func() {
				s3Client.EXPECT().
					CreateMultipartUpload(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&s3sdk.CreateMultipartUploadOutput{
						Bucket:   aws.String("testBucket"),
						Key:      aws.String("testKey"),
						UploadId: aws.String("testUploadId"),
					}, nil)
				s3Client.EXPECT().UploadPart(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&s3sdk.UploadPartOutput{}, nil)
				s3Client.EXPECT().CompleteMultipartUpload(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(&s3sdk.CompleteMultipartUploadOutput{}, nil)
			},
			wantErr: false,
		},
		{
			name:   "error",
			fields: fields{Client: s3Client},
			args: args{
				input: s3sdk.CreateMultipartUploadInput{
					ACL:    types.ObjectCannedACL("testACL"),
					Bucket: aws.String("testBucket"),
					Key:    aws.String("testKey"),
				},
				reader: file,
			},
			prepare: func() {
				s3Client.EXPECT().
					CreateMultipartUpload(gomock.Any(), gomock.Any(), gomock.Any()).
					Return(nil, errors.New("error 1"))
			},
			wantErr: true,
		},
	}
	for _, tt := range tests {
		tt := tt
		t.Run(tt.name, func(t *testing.T) {
			tt.prepare()
			s := ceph2.News3Client(tt.fields.Client)
			if err := s.Upload(ctx, tt.args.input, tt.args.reader, nil); (err != nil) != tt.wantErr {
				t.Errorf("Upload() error = %v, wantErr %v", err, tt.wantErr)
			}
		})
	}
}
