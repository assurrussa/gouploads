package s3store_test

import (
	"bytes"
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
	s3storemocks "github.com/assurrussa/gouploads/infrastructure/storage/files/s3store/mocks"
)

func TestUpload(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	client := s3storemocks.NewMockMultiPartUploader(ctrl)
	input := stubInput()

	tests := []struct {
		name      string
		prepare   func(ctx context.Context, cancel context.CancelFunc)
		wantError string
	}{
		{
			name: "Success",
			prepare: func(ctx context.Context, _ context.CancelFunc) {
				client.EXPECT().
					CreateMultipartUpload(ctx, gomock.Any()).
					Return(&s3.CreateMultipartUploadOutput{}, nil)

				eTag := "ETag"
				client.EXPECT().
					UploadPart(ctx, gomock.Any()).
					Return(&s3.UploadPartOutput{ETag: &eTag}, nil)

				client.EXPECT().
					CompleteMultipartUpload(ctx, gomock.Any()).
					Return(nil, nil)
			},
			wantError: "",
		},
		{
			name: "CantCreateMultipartError",
			prepare: func(ctx context.Context, _ context.CancelFunc) {
				client.EXPECT().
					CreateMultipartUpload(ctx, gomock.Any()).
					Return(nil, errors.New("test-error"))
			},
			wantError: "can't create multipart: test-error",
		},
		{
			name: "CantAbortUploadError",
			prepare: func(ctx context.Context, _ context.CancelFunc) {
				client.EXPECT().
					CreateMultipartUpload(ctx, gomock.Any()).
					Return(&s3.CreateMultipartUploadOutput{}, nil)

				client.EXPECT().
					UploadPart(ctx, gomock.Any()).
					Return(nil, errors.New("test-error"))

				client.EXPECT().
					AbortMultipartUpload(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("test-error"))
			},
			wantError: "can't upload part #1 of file `Key`: test-error: can't abort upload: test-error",
		},
		{
			name: "CantCompleteMultipartUploadOfFile_AbortUpload",
			prepare: func(ctx context.Context, _ context.CancelFunc) {
				client.EXPECT().
					CreateMultipartUpload(ctx, gomock.Any()).
					Return(&s3.CreateMultipartUploadOutput{}, nil)

				eTag := "ETag"
				client.EXPECT().
					UploadPart(ctx, gomock.Any()).
					Return(&s3.UploadPartOutput{ETag: &eTag}, nil)

				client.EXPECT().
					CompleteMultipartUpload(ctx, gomock.Any()).
					Return(nil, errors.New("test-error"))

				client.EXPECT().
					AbortMultipartUpload(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("test-error"))
			},
			wantError: "can't complete multipart upload of file `Key`: test-error: can't abort upload: test-error",
		},
		{
			name: "ErrorContextHasBeenCanceled_AbortUpload",
			prepare: func(ctx context.Context, cancel context.CancelFunc) {
				cancel()
				client.EXPECT().
					CreateMultipartUpload(ctx, gomock.Any()).
					Return(&s3.CreateMultipartUploadOutput{}, nil)

				client.EXPECT().
					AbortMultipartUpload(gomock.Any(), gomock.Any()).
					Return(nil, errors.New("test-error"))
			},
			wantError: "context has been canceled before upload has done: context canceled: can't abort upload: test-error",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()

			tt.prepare(ctx, cancel)

			reader := strings.NewReader("test")

			if err := s3store.Upload(ctx, client, input, reader); err != nil {
				assert.EqualError(t, err, tt.wantError)
			} else {
				assert.NoError(t, err)
			}
		})
	}
}

func stubInput() *s3.CreateMultipartUploadInput {
	key := "Key"

	return &s3.CreateMultipartUploadInput{Key: &key}
}

func TestUpload_DoesNotUploadEmptyPart(t *testing.T) {
	ctrl := gomock.NewController(t)
	defer ctrl.Finish()

	ctx := context.Background()
	client := s3storemocks.NewMockMultiPartUploader(ctrl)
	key := "Key"
	bucket := "bucket"
	uploadID := "upload-id"

	client.EXPECT().
		CreateMultipartUpload(ctx, gomock.Any()).
		Return(&s3.CreateMultipartUploadOutput{
			Bucket:   aws.String(bucket),
			Key:      aws.String(key),
			UploadId: aws.String(uploadID),
		}, nil)

	eTag := "etag"
	client.EXPECT().
		UploadPart(ctx, gomock.Any()).
		DoAndReturn(func(_ context.Context, input *s3.UploadPartInput, _ ...func(*s3.Options)) (*s3.UploadPartOutput, error) {
			if input == nil || input.Body == nil {
				t.Fatalf("expected body in upload part")
			}
			if input.ContentLength == nil || *input.ContentLength == 0 {
				t.Fatalf("expected non-zero content length")
			}
			return &s3.UploadPartOutput{ETag: &eTag}, nil
		})

	client.EXPECT().
		CompleteMultipartUpload(ctx, gomock.Any()).
		Return(&s3.CompleteMultipartUploadOutput{}, nil)

	data := bytes.Repeat([]byte("a"), s3store.MinPartSize)
	reader := bytes.NewReader(data)

	input := &s3.CreateMultipartUploadInput{Key: &key}
	require.NoError(t, s3store.Upload(ctx, client, input, reader))
}
