package uploadfilejob_test

import (
	"context"
	"errors"
	"testing"

	"github.com/assurrussa/goshared/pkg/logger"
	"github.com/assurrussa/goshared/pkg/tests"
	sharedoutbox "github.com/assurrussa/outbox/outbox"
	outboxmocks "github.com/assurrussa/outbox/outbox/mocks"
	"github.com/assurrussa/outbox/outbox/models"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	uploadfilejob "github.com/assurrussa/gouploads/domain/files/outbox/upload_file"
	uploadsmocks "github.com/assurrussa/gouploads/domain/files/outbox/upload_file/mocks"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
)

type TestSuite struct {
	suite.Suite

	uploadFileUseCaseMock *uploadsmocks.MockuploadFileUseCase

	job           *uploadfilejob.Job
	expectedError error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		uploadFileUseCaseMock := uploadsmocks.NewMockuploadFileUseCase(ctrl)

		job := uploadfilejob.Must(uploadfilejob.NewOptions(uploadFileUseCaseMock, log))

		return &TestSuite{
			job:                   job,
			uploadFileUseCaseMock: uploadFileUseCaseMock,
			expectedError:         errors.New("expected error"),
		}
	})
}

func TestJob_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		uploadfilejob.Must(uploadfilejob.NewOptions(nil, nil))
	})
}

func TestJobHandle_Name(t *testing.T) {
	_, _, ts := NewTestSuite(t)

	ts.Equal(uploadfilejob.JobName, ts.job.Name())
}

func TestJobHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileID := int64(123)
	preset := "original"
	url := "https://example.com/path/to/file.txt"
	payload, err := uploadfilejob.MarshalPayload(uploadfilejob.NewPayload(fileID, []uploadfilejob.Artifact{
		{Preset: preset, URL: url, MediaType: "image", ContentType: "image/jpeg"},
	}))
	ts.Require().NoError(err)

	ts.uploadFileUseCaseMock.EXPECT().Handle(ctx, uploadfile.Request{
		FileID: fileID,
		Artifacts: []uploadfile.Artifact{
			{Preset: preset, URL: url, MediaType: "image", ContentType: "image/jpeg"},
		},
	}).Return(uploadfile.Response{}, nil).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().NoError(err)
}

func TestJobHandle_Error(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileID := int64(123)
	preset := "original"
	url := "https://example.com/path/to/file.txt"
	payload, err := uploadfilejob.MarshalPayload(uploadfilejob.NewPayload(fileID, []uploadfilejob.Artifact{
		{Preset: preset, URL: url, MediaType: "image", ContentType: "image/jpeg"},
	}))
	ts.Require().NoError(err)

	ts.uploadFileUseCaseMock.EXPECT().Handle(ctx, uploadfile.Request{
		FileID: fileID,
		Artifacts: []uploadfile.Artifact{
			{Preset: preset, URL: url, MediaType: "image", ContentType: "image/jpeg"},
		},
	}).Return(uploadfile.Response{}, ts.expectedError).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().ErrorIs(err, ts.expectedError)
}

func TestJobHandle_ErrorPayload(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	err := ts.job.Handle(ctx, `{payload: 1'`)
	ts.Require().Error(err)
}

func TestJobHandle_CleanupOnlyOnTerminalOutboxAttempt(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		attempt     int
		wantCleanup bool
	}{
		{name: "transient", attempt: 29},
		{name: "terminal", attempt: 30, wantCleanup: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			ctrl := gomock.NewController(t)
			uploadUseCase := uploadsmocks.NewMockuploadFileUseCase(ctrl)
			jobsRepo := outboxmocks.NewMockJobsRepository(ctrl)
			failedRepo := outboxmocks.NewMockJobsFailedRepository(ctrl)
			transactor := outboxmocks.NewMockTransactor(ctrl)
			job := uploadfilejob.Must(uploadfilejob.NewOptions(uploadUseCase, logger.Discard()))

			payload, err := uploadfilejob.MarshalPayload(uploadfilejob.NewPayload(123, []uploadfilejob.Artifact{{
				Preset:      "main",
				URL:         "https://resizer.example.test/main.png",
				ContentType: "image/png",
			}}))
			require.NoError(t, err)

			jobID := outboxtypes.NewJobID()
			jobsRepo.EXPECT().FindAndReserveJob(gomock.Any(), gomock.Any(), gomock.Any()).Return(models.Job{
				ID:       jobID,
				Name:     uploadfilejob.JobName,
				Payload:  payload,
				Attempts: tt.attempt,
			}, nil).Times(1)
			jobsRepo.EXPECT().DeleteJob(gomock.Any(), jobID).Return(int64(1), nil).Times(1)

			ctx, cancel := context.WithCancel(context.Background())
			uploadUseCase.EXPECT().Handle(gomock.Any(), uploadfile.Request{
				FileID: 123,
				Artifacts: []uploadfile.Artifact{{
					Preset:      "main",
					URL:         "https://resizer.example.test/main.png",
					ContentType: "image/png",
				}},
				CleanupOnFailure: tt.wantCleanup,
			}).DoAndReturn(func(context.Context, uploadfile.Request) (uploadfile.Response, error) {
				cancel()
				return uploadfile.Response{}, nil
			}).Times(1)

			service, err := sharedoutbox.New(
				sharedoutbox.WithJobsRepo(jobsRepo),
				sharedoutbox.WithJobsFailedRepo(failedRepo),
				sharedoutbox.WithTransactor(transactor),
			)
			require.NoError(t, err)
			service.MustRegisterJob(job)
			require.NoError(t, service.Run(ctx))
		})
	}
}
