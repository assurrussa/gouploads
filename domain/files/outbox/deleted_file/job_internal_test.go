package deletedfilejob_test

import (
	"context"
	"errors"
	"testing"

	"github.com/assurrussa/goshared/pkg/logger"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/assurrussa/goshared/pkg/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	deletedfilejob "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	deletedfilejobmocks "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file/mocks"
	"github.com/assurrussa/gouploads/domain/files/shared"
	usecase "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
)

type TestSuite struct {
	suite.Suite

	useCaseMock *deletedfilejobmocks.MockdeleteFileUseCase

	job         *deletedfilejob.Job
	expectError error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		useCaseMock := deletedfilejobmocks.NewMockdeleteFileUseCase(ctrl)

		job := deletedfilejob.Must(deletedfilejob.NewOptions(useCaseMock, log))

		return &TestSuite{
			job:         job,
			useCaseMock: useCaseMock,
			expectError: errors.New("test error"),
		}
	})
}

func TestJob_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		deletedfilejob.Must(deletedfilejob.NewOptions(nil, nil))
	})
}

func TestJobHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	ts.Equal(deletedfilejob.JobName, ts.job.Name())

	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	eventsAfter := shared.NewFileEventAfterJobs("model_deleted_bind", userID, map[string]any{"foo": "bar"})
	payload, err := deletedfilejob.MarshalPayload(deletedfilejob.NewOwnedPayload(
		fileID,
		userID,
		shared.ObjectTypeAdmin,
		shared.FileObjectID(42),
		file.GetFullPath(),
		eventsAfter...,
	))
	ts.Require().NoError(err)

	ts.useCaseMock.EXPECT().Handle(ctx, usecase.Request{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    file.GetFullPath(),
		ObjectType:  shared.ObjectTypeAdmin,
		ObjectID:    shared.FileObjectID(42),
		AfterEvents: eventsAfter,
	}).Return(usecase.Response{}, nil).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().NoError(err)
}

func TestJobHandle_Error_UseCase(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	userID := sharedtypes.MustParse[sharedtypes.UserID]("368c1d49-713e-4b7f-9e8c-bd2b73b1d274")
	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	eventsAfter := shared.NewFileEventAfterJobs("model_deleted_bind", userID, map[string]any{"foo": "bar"})
	payload, err := deletedfilejob.MarshalPayload(deletedfilejob.NewPayload(fileID, userID, file.GetFullPath(), eventsAfter...))
	ts.Require().NoError(err)

	ts.useCaseMock.EXPECT().Handle(ctx, usecase.Request{
		UserID:      userID,
		FileID:      fileID,
		FilePath:    file.GetFullPath(),
		AfterEvents: eventsAfter,
	}).Return(usecase.Response{}, ts.expectError).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().ErrorIs(err, ts.expectError)
}

func TestJobHandle_Error_payload(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	err := ts.job.Handle(ctx, `{asfasfqwer:""`)
	ts.Require().Error(err)
}
