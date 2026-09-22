package uploadrawfile_test

import (
	"context"
	"errors"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/assurrussa/goshared/pkg/tests"
	eventstreammocks "github.com/assurrussa/goshared/services/event-stream/mocks"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	uploadrawfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_raw_file"
	uploadrawfilemocks "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_raw_file/mocks"
)

type TestSuite struct {
	suite.Suite

	uploadServiceMock *uploadrawfilemocks.MockuploadService
	eventStreamMock   *eventstreammocks.MockEventStream

	useCase       *uploadrawfile.UseCase
	expectedError error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		uploadServiceMock := uploadrawfilemocks.NewMockuploadService(ctrl)
		eventStreamMock := eventstreammocks.NewMockEventStream(ctrl)

		useCase := uploadrawfile.Must(uploadrawfile.NewOptions(
			uploadServiceMock,
			eventStreamMock,
			log,
		))

		return &TestSuite{
			useCase:           useCase,
			uploadServiceMock: uploadServiceMock,
			eventStreamMock:   eventStreamMock,
			expectedError:     errors.New("expected error"),
		}
	})
}

func TestHandle_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		uploadrawfile.Must(uploadrawfile.NewOptions(nil, nil, nil))
	})
}

func TestHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	adminUUID := sharedtypes.NewUserID()
	fileHeader := testshelpers.MakeFileHeaderImage(t, "photo", "cover.png", "image/png")
	req := uploadrawfile.Request{
		UploaderUUID: adminUUID,
		RequestID:    sharedtypes.NewRequestID(),
		ManagerID:    1,
		UserID:       3,
		FileHeader:   fileHeader,
		ObjectType:   shared.ObjectTypeAdmin,
		ObjectID:     shared.FileObjectID(12344),
		DeletedID:    shared.FileObjectID(12343),
		AfterJobs: []shared.FileEventAfterJob{
			{JobName: "any_name", UserID: sharedtypes.NewUserID()},
		},
		Config: &uploadservice.FileUploadConfig{},
	}
	ts.uploadServiceMock.EXPECT().UploadSingle(ctx, uploadservice.SingleRequest{
		UploaderUUID: req.UploaderUUID,
		ManagerID:    req.ManagerID,
		UserID:       req.UserID,
		FileHeader:   req.FileHeader,
		ObjectType:   req.ObjectType,
		ObjectID:     req.ObjectID,
		DeletedID:    req.DeletedID,
		AfterJobs:    req.AfterJobs,
		Config:       req.Config,
	}).Return(model.File{ID: 123}, nil).Times(1)

	resp, err := ts.useCase.Handle(ctx, req)
	ts.Require().NoError(err)
	ts.NotEmpty(resp)
}

func TestHandle_Error(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileHeader := testshelpers.MakeFileHeaderImage(t, "photo", "cover.png", "image/png")
	req := uploadrawfile.Request{
		RequestID:    sharedtypes.NewRequestID(),
		UploaderUUID: sharedtypes.NewUserID(),
		ManagerID:    1,
		UserID:       3,
		FileHeader:   fileHeader,
		ObjectType:   shared.ObjectTypeAdmin,
		ObjectID:     shared.FileObjectID(12344),
		DeletedID:    shared.FileObjectID(12343),
		AfterJobs: []shared.FileEventAfterJob{
			{JobName: "any_name", UserID: sharedtypes.NewUserID()},
		},
		Config: &uploadservice.FileUploadConfig{},
	}
	ts.uploadServiceMock.EXPECT().UploadSingle(ctx, gomock.Any()).Return(model.File{ID: 123}, ts.expectedError).Times(1)

	resp, err := ts.useCase.Handle(ctx, req)
	ts.Require().ErrorIs(err, ts.expectedError)
	ts.Empty(resp)
}

func TestHandle_ErrorEmpty(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	req := uploadrawfile.Request{}

	resp, err := ts.useCase.Handle(ctx, req)
	ts.Require().Error(err)
	ts.Empty(resp)
}
