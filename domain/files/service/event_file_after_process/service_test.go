package eventfileafterprocess_test

import (
	"context"
	"errors"
	"fmt"
	"testing"

	logger "github.com/assurrussa/gologger"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/assurrussa/goshared/pkg/tests"
	eventstreammocks "github.com/assurrussa/goshared/services/event-stream/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	eventfileafterprocess "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process"
	eventfileafterprocessmocks "github.com/assurrussa/gouploads/domain/files/service/event_file_after_process/mocks"
	"github.com/assurrussa/gouploads/domain/files/shared"
)

type TestSuite struct {
	suite.Suite

	fileMock        *eventfileafterprocessmocks.MockfileRepository
	transactorMock  *eventfileafterprocessmocks.Mocktransactor
	eventStreamMock *eventstreammocks.MockEventStream

	job *eventfileafterprocess.Service
}

func NewTestSuite(
	t *testing.T,
	fnCalls ...eventfileafterprocess.FnCallAfterProcess,
) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		transactorMock := eventfileafterprocessmocks.NewMocktransactor(ctrl)
		fileMock := eventfileafterprocessmocks.NewMockfileRepository(ctrl)
		eventStreamMock := eventstreammocks.NewMockEventStream(ctrl)

		var fnCall eventfileafterprocess.FnCallAfterProcess
		for _, fn := range fnCalls {
			fnCall = fn
			break
		}

		job := eventfileafterprocess.Must(eventfileafterprocess.NewOptions(
			transactorMock,
			fileMock,
			eventStreamMock,
			log,
			eventfileafterprocess.WithFnCallAfterProcess(fnCall),
		))

		return &TestSuite{
			job:             job,
			fileMock:        fileMock,
			transactorMock:  transactorMock,
			eventStreamMock: eventStreamMock,
		}
	})
}

func TestJob_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		eventfileafterprocess.Must(eventfileafterprocess.NewOptions(nil, nil, nil, nil))
	})
}

func TestJobHandle_Success(t *testing.T) {
	fileID := int64(123)
	userID := sharedtypes.NewUserID()

	ctx, _, ts := NewTestSuite(t, func(_ context.Context, data eventfileafterprocess.Payload, file model.File) error {
		assert.Equal(t, userID, data.UserID)
		assert.Equal(t, fileID, data.FileID)
		assert.Equal(t, fileID, file.ID)
		return nil
	})

	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	payload, err := eventfileafterprocess.MarshalPayload(eventfileafterprocess.NewPayload(
		userID, shared.UserTypeAdmin, fileID, "testEventJob",
	))
	ts.Require().NoError(err)
	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByID(ctx, fileID).Return(file, nil).Times(1)

	eventFileUpload := shared.NewEventAfterProcess(
		fileID, file.GetPublicURL(), shared.StatusCompleted, "testEventJob",
	)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, userID, mockWantPublishFormatter("file publish success matcher", eventFileUpload)).
		Return(nil).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().NoError(err)
}

func TestJobHandle_SuccessWithFnCall(t *testing.T) {
	fileID := int64(123)
	userID := sharedtypes.NewUserID()

	ctx, _, ts := NewTestSuite(t)

	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	payload, err := eventfileafterprocess.MarshalPayload(eventfileafterprocess.NewPayload(
		userID, shared.UserTypeAdmin, fileID, "testEventJob",
	))
	ts.Require().NoError(err)

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByID(ctx, fileID).Return(file, nil).Times(1)

	eventFileUpload := shared.NewEventAfterProcess(
		fileID, file.GetPublicURL(), shared.StatusCompleted, "testEventJob",
	)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, userID, mockWantPublishFormatter("file publish success matcher", eventFileUpload)).
		Return(nil).Times(1)

	err = ts.job.HandleAfterProcess(
		ctx, payload, func(_ context.Context, data eventfileafterprocess.Payload, file model.File) error {
			ts.Equal(userID, data.UserID)
			ts.Equal(fileID, data.FileID)
			ts.Equal(fileID, file.ID)
			return nil
		},
	)
	ts.Require().NoError(err)
}

func TestJobHandle_SuccessEmptyFn(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	err := ts.job.Handle(ctx, "asfsaf")
	ts.Require().NoError(err)
}

func TestJobHandle_SuccessUserIDIsZero(t *testing.T) {
	ctx, _, ts := NewTestSuite(t, func(_ context.Context, _ eventfileafterprocess.Payload, _ model.File) error {
		return nil
	})

	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	userID := sharedtypes.UserIDNil
	payload, err := eventfileafterprocess.MarshalPayload(eventfileafterprocess.NewPayload(
		userID, shared.UserTypeAdmin, fileID, "testEventJob",
	))
	ts.Require().NoError(err)

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByID(ctx, fileID).Return(file, nil).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().NoError(err)
}

func TestJobHandle_ErrorOutbox(t *testing.T) {
	ctx, _, ts := NewTestSuite(t, func(_ context.Context, _ eventfileafterprocess.Payload, _ model.File) error {
		return nil
	})

	errExpect := errors.New("expected error")
	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	userID := sharedtypes.NewUserID()
	payload, err := eventfileafterprocess.MarshalPayload(eventfileafterprocess.NewPayload(
		userID, shared.UserTypeAdmin, fileID, "testEventJob",
	))
	ts.Require().NoError(err)

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByID(ctx, fileID).Return(file, nil).Times(1)

	eventFileUpload := shared.NewEventAfterProcess(
		fileID, file.GetPublicURL(), shared.StatusCompleted, "testEventJob",
	)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, userID, mockWantPublishFormatter("file 2 publish success matcher", eventFileUpload)).
		Return(errExpect).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().NoError(err)
}

func TestJobHandle_ErrorFnCall(t *testing.T) {
	errExpected := errors.New("expected error")
	ctx, _, ts := NewTestSuite(t, func(_ context.Context, _ eventfileafterprocess.Payload, _ model.File) error {
		return errExpected
	})

	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	userID := sharedtypes.NewUserID()
	payload, err := eventfileafterprocess.MarshalPayload(eventfileafterprocess.NewPayload(
		userID, shared.UserTypeAdmin, fileID, "testEventJob",
	))
	ts.Require().NoError(err)

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByID(ctx, fileID).Return(file, nil).Times(1)

	eventFileUpload := shared.NewEventAfterProcess(
		fileID, file.GetPublicURL(), shared.StatusFailed, "testEventJob",
	)
	eventFileUpload.Error = "trx: fn call after process: " + errExpected.Error()
	ts.eventStreamMock.EXPECT().
		Publish(ctx, userID, mockWantPublishFormatter("file 3 publish success matcher", eventFileUpload)).
		Return(nil).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().ErrorIs(err, errExpected)
}

func TestJobHandle_ErrorFileGetByID(t *testing.T) {
	ctx, _, ts := NewTestSuite(t, func(_ context.Context, _ eventfileafterprocess.Payload, _ model.File) error {
		return nil
	})

	errExpect := errors.New("expected error")
	fileID := int64(123)
	file := model.File{
		ID:         fileID,
		FileName:   "testname.png",
		FolderPath: "path/foo",
	}

	userID := sharedtypes.NewUserID()
	payload, err := eventfileafterprocess.MarshalPayload(eventfileafterprocess.NewPayload(
		userID, shared.UserTypeAdmin, fileID, "testEventJob",
	))
	ts.Require().NoError(err)

	ts.fileMock.EXPECT().GetByID(ctx, fileID).Return(file, errExpect).Times(1)

	err = ts.job.Handle(ctx, payload)
	ts.Require().ErrorIs(err, errExpect)
}

func TestJobHandle_ErrorPayload(t *testing.T) {
	ctx, _, ts := NewTestSuite(t, func(_ context.Context, _ eventfileafterprocess.Payload, _ model.File) error {
		return nil
	})

	payload := `{"asfasf":12424124`
	err := ts.job.Handle(ctx, payload)
	ts.Require().Error(err)
}

func mockWantPublishFormatter(name string, event shared.EventAfterProcess) gomock.Matcher {
	var unequalFields []string
	return gomock.WantFormatter(
		gomock.StringerFunc(func() string { return fmt.Sprintf("diff[%v]\n %v - %s", unequalFields, event, name) }),
		gomock.Cond(func(x shared.EventAfterProcess) bool {
			if x.EventType != event.EventType {
				unequalFields = append(unequalFields, "EventType")
			}
			if x.FileID != event.FileID {
				unequalFields = append(unequalFields, "FileID")
			}
			if x.Status != event.Status {
				unequalFields = append(unequalFields, "Status")
			}
			if x.Error != event.Error {
				unequalFields = append(unequalFields, "Error")
			}
			if x.FilePath != event.FilePath {
				unequalFields = append(unequalFields, "FilePath")
			}

			return len(unequalFields) == 0
		}),
	)
}
