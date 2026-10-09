package s3store_test

import (
	"context"
	"errors"
	"strings"
	"testing"
	"testing/iotest"
	"testing/synctest"
	"time"

	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/infrastructure/storage/files/s3store"
	s3storemocks "github.com/assurrussa/gouploads/infrastructure/storage/files/s3store/mocks"
)

// Cleanup must start despite parent cancellation, then stop at its own deadline.
// Both the original failure and a cleanup timeout must survive error wrapping.
func TestUploadAbortContext(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded, errors.New("read failed")} {
		t.Run(cause.Error(), func(t *testing.T) {
			synctest.Test(t, func(t *testing.T) {
				ctrl := gomock.NewController(t)
				client := s3storemocks.NewMockMultiPartUploader(ctrl)
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if errors.Is(cause, context.DeadlineExceeded) {
					ctx, cancel = context.WithDeadline(ctx, time.Now())
					defer cancel()
				}
				client.EXPECT().CreateMultipartUpload(ctx, gomock.Any()).DoAndReturn(
					func(context.Context, *s3.CreateMultipartUploadInput, ...func(*s3.Options)) (*s3.CreateMultipartUploadOutput, error) {
						if errors.Is(cause, context.Canceled) {
							cancel()
						}
						return &s3.CreateMultipartUploadOutput{}, nil
					})
				var cleanup context.Context
				client.EXPECT().AbortMultipartUpload(gomock.Any(), gomock.Any()).DoAndReturn(
					func(abortCtx context.Context, _ *s3.AbortMultipartUploadInput,
						_ ...func(*s3.Options),
					) (*s3.AbortMultipartUploadOutput, error) {
						cleanup = abortCtx
						require.NoError(t, abortCtx.Err(), "cleanup starts uncancelled")
						deadline, ok := abortCtx.Deadline()
						require.True(t, ok, "cleanup requires an independent deadline")
						require.Positive(t, time.Until(deadline))
						require.LessOrEqual(t, time.Until(deadline), 5*time.Second)
						<-abortCtx.Done()
						return nil, abortCtx.Err()
					})
				err := s3store.Upload(ctx, client, stubInput(), iotest.ErrReader(cause))
				require.ErrorIs(t, err, cause)
				require.ErrorIs(t, err, context.DeadlineExceeded)
				require.ErrorIs(t, cleanup.Err(), context.DeadlineExceeded)
			})
		})
	}
}

func TestUploadAbortReleasesContext(t *testing.T) {
	ctrl := gomock.NewController(t)
	client := s3storemocks.NewMockMultiPartUploader(ctrl)
	ctx := context.Background()
	cause := errors.New("part failed")
	client.EXPECT().CreateMultipartUpload(ctx, gomock.Any()).Return(&s3.CreateMultipartUploadOutput{}, nil)
	client.EXPECT().UploadPart(ctx, gomock.Any()).Return(nil, cause)
	var cleanup context.Context
	client.EXPECT().AbortMultipartUpload(gomock.Any(), gomock.Any()).DoAndReturn(
		func(abortCtx context.Context, _ *s3.AbortMultipartUploadInput,
			_ ...func(*s3.Options),
		) (*s3.AbortMultipartUploadOutput, error) {
			cleanup = abortCtx
			require.NoError(t, abortCtx.Err())
			return &s3.AbortMultipartUploadOutput{}, nil
		})
	err := s3store.Upload(ctx, client, stubInput(), strings.NewReader("part"))
	require.ErrorIs(t, err, cause)
	require.ErrorIs(t, cleanup.Err(), context.Canceled, "successful abort releases its timer")
}

func TestUploadCancellationIdentity(t *testing.T) {
	for _, deadline := range []bool{false, true} {
		name := "cancel"
		if deadline {
			name = "deadline"
		}
		t.Run(name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			client := s3storemocks.NewMockMultiPartUploader(ctrl)
			ctx, cancel := context.WithCancel(context.Background())
			if deadline {
				cancel()
				ctx, cancel = context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
			}
			cancel()
			client.EXPECT().CreateMultipartUpload(ctx, gomock.Any()).Return(&s3.CreateMultipartUploadOutput{}, nil)
			client.EXPECT().AbortMultipartUpload(gomock.Any(), gomock.Any()).Return(nil, nil)
			err := s3store.Upload(ctx, client, stubInput(), strings.NewReader("part"))
			require.ErrorIs(t, err, ctx.Err())
		})
	}
}
