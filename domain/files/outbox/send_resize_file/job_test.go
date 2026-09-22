package sendresizefilejob_test

import (
	"context"
	"errors"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/goshared/pkg/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	sendresizefilejob "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	sendresizefilejobmocks "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file/mocks"
	usecase "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
)

type TestSuite struct {
	suite.Suite

	useCaseMock *sendresizefilejobmocks.MocksendResizeFileUseCase

	job         *sendresizefilejob.Job
	expectError error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		useCaseMock := sendresizefilejobmocks.NewMocksendResizeFileUseCase(ctrl)

		job := sendresizefilejob.Must(sendresizefilejob.NewOptions(useCaseMock, log))

		return &TestSuite{
			job:         job,
			useCaseMock: useCaseMock,
			expectError: errors.New("test error"),
		}
	})
}

func TestJob_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		sendresizefilejob.Must(sendresizefilejob.NewOptions(nil, nil))
	})
}

func TestJobHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	ts.Equal(sendresizefilejob.JobName, ts.job.Name())

	file := model.File{
		ID:         123,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		file.ID, file.GetFullPath(), false,
	))
	ts.Require().NoError(err)

	ts.useCaseMock.EXPECT().Handle(ctx, usecase.Request{
		FileID:   file.ID,
		FilePath: file.GetFullPath(),
	}).Return(usecase.Response{}, nil).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().NoError(err)
}

func TestJobHandle_SuccessSkipResizeVideo(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	ts.Equal(sendresizefilejob.JobName, ts.job.Name())

	file := model.File{
		ID:         123,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		file.ID, file.GetFullPath(), true,
	))
	ts.Require().NoError(err)

	ts.useCaseMock.EXPECT().Handle(ctx, usecase.Request{
		FileID:          file.ID,
		FilePath:        file.GetFullPath(),
		SkipResizeVideo: true,
	}).Return(usecase.Response{}, nil).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().NoError(err)
}

func TestJobHandle_Error_UseCase(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	file := model.File{
		ID:         123,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		file.ID, file.GetFullPath(), false,
	))
	ts.Require().NoError(err)

	ts.useCaseMock.EXPECT().Handle(ctx, usecase.Request{
		FileID:   file.ID,
		FilePath: file.GetFullPath(),
	}).Return(usecase.Response{}, ts.expectError).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().ErrorIs(err, ts.expectError)
}

func TestJobHandle_Error_payload(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	err := ts.job.Handle(ctx, `{asfasfqwer:""`)
	ts.Require().Error(err)
}
