package listenresizefile_test

import (
	"context"
	"errors"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/goshared/pkg/tests"
	eventstreammocks "github.com/assurrussa/gowebsocket/eventstream/mocks"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	testsmatcher "github.com/assurrussa/gouploads/domain/files/tests/matcher"
	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
	listenresizefilemocks "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file/mocks"
)

type TestSuite struct {
	suite.Suite

	fileRepositoryMock *listenresizefilemocks.MockfileRepository
	outboxPutterMock   *listenresizefilemocks.MockoutboxPutter
	eventStreamMock    *eventstreammocks.MockEventStream

	useCase       *listenresizefile.UseCase
	expectedError error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		fileRepositoryMock := listenresizefilemocks.NewMockfileRepository(ctrl)
		outboxPutterMock := listenresizefilemocks.NewMockoutboxPutter(ctrl)
		eventStreamMock := eventstreammocks.NewMockEventStream(ctrl)

		useCase := listenresizefile.Must(listenresizefile.NewOptions(
			fileRepositoryMock,
			outboxPutterMock,
			eventStreamMock,
			log,
		))

		return &TestSuite{
			useCase:            useCase,
			fileRepositoryMock: fileRepositoryMock,
			eventStreamMock:    eventStreamMock,
			outboxPutterMock:   outboxPutterMock,
			expectedError:      errors.New("expected error"),
		}
	})
}

func TestHandle_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		listenresizefile.Must(listenresizefile.NewOptions(nil, nil, nil, nil))
	})
}

func TestHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileModel := testshelpers.CreateFile(t)
	req := listenresizefile.Request{
		ExternalID: fileModel.ID,
		Status:     "done",
		Error:      "",
		Metadata:   map[string]any{"taskId": "1234"},
		Artifacts: []listenresizefile.Artifact{
			{Preset: "original", URL: "https://resizer.example.com/original.png", Metadata: map[string]any{"fileId": "123456"}},
			{Preset: "small", URL: "https://resizer.example.com/small.png"},
		},
	}
	eventFileSendResizer := createEvent(fileModel, req, shared.FileUploadTaskStatusProcessing)
	//nolint:lll // tests
	payloadPut := `{"fileId":123456,"artifacts":[{"preset":"original","url":"https://resizer.example.com/original.png","expireAt":"0001-01-01T00:00:00Z","metadata":{"fileId":"123456"}},{"preset":"small","url":"https://resizer.example.com/small.png","expireAt":"0001-01-01T00:00:00Z"}]}`

	ts.outboxPutterMock.EXPECT().Put(gomock.Any(), "upload_file_persist", payloadPut, gomock.Any()).
		Return(outboxtypes.NewJobID(), nil).Times(1)
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, nil).Times(1)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, fileModel.GetData().Uploader.UserUUID, testsmatcher.NewEventPublishMatcher(
			"file publish create matcher", eventFileSendResizer,
		)).
		Return(ts.expectedError).Times(1)

	resp, err := ts.useCase.Handle(ctx, req)
	ts.Require().NoError(err)
	ts.Empty(resp)
}

func TestHandle_ErrorOutboxPut(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileModel := testshelpers.CreateFile(t)
	req := listenresizefile.Request{
		ExternalID: fileModel.ID,
		Status:     "done",
		Error:      "",
		Metadata:   map[string]any{"fileId": "1234"},
		Artifacts: []listenresizefile.Artifact{
			{Preset: "original", URL: "https://resizer.example.com/original.png"},
			{Preset: "small", URL: "https://resizer.example.com/small.png"},
		},
	}

	ts.outboxPutterMock.EXPECT().Put(gomock.Any(), "upload_file_persist", gomock.Any(), gomock.Any()).
		Return(outboxtypes.NewJobID(), ts.expectedError).Times(1)
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, nil).Times(1)

	resp, err := ts.useCase.Handle(ctx, req)
	ts.Require().ErrorIs(err, ts.expectedError)
	ts.Empty(resp)
}

func TestHandle_ErrorGetByID(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileModel := testshelpers.CreateFile(t)
	req := listenresizefile.Request{
		ExternalID: fileModel.ID,
		Status:     "done",
		Error:      "",
		Metadata:   map[string]any{"taskId": "1234"},
		Artifacts: []listenresizefile.Artifact{
			{
				Preset:      "original",
				URL:         "https://resizer.example.com/original.png",
				Metadata:    map[string]any{"fileId": "123456"},
				MediaType:   "image",
				ContentType: "image/png",
			},
			{Preset: "small", URL: "https://resizer.example.com/small.png"},
		},
	}
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, ts.expectedError).Times(1)

	resp, err := ts.useCase.Handle(ctx, req)
	ts.Require().ErrorIs(err, ts.expectedError)
	ts.Empty(resp)
}

func TestHandle_ErrorState(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileModel := testshelpers.CreateFile(t)
	req := listenresizefile.Request{
		ExternalID: fileModel.ID,
		Status:     "another_state",
		Artifacts: []listenresizefile.Artifact{
			{Preset: "original", URL: "https://resizer.example.com/original.png"},
		},
	}

	resp, err := ts.useCase.Handle(ctx, req)
	ts.Require().NoError(err)
	ts.Empty(resp)
}

func TestHandle_ErrorEmpty(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	req := listenresizefile.Request{}

	resp, err := ts.useCase.Handle(ctx, req)
	ts.Require().Error(err)
	ts.Empty(resp)
}

func createEvent(
	task model.File,
	req listenresizefile.Request,
	status shared.FileUploadTaskStatus,
) shared.FileUploadStatusEvent {
	presets := make([]string, 0, len(req.Artifacts))
	for _, artifact := range req.Artifacts {
		presets = append(presets, artifact.Preset)
	}

	eventFileSendResizer := shared.NewFileUploadStatusEvent(task.ID, status)
	eventFileSendResizer.Metadata = map[string]any{
		"jobState":     req.Status,
		"presets":      presets,
		"uploaderUuid": task.GetData().Uploader.UserUUID.String(),
		"objectType":   task.ObjectType.String(),
		"objectId":     task.ObjectID.String(),
	}
	eventFileSendResizer.File = &shared.FileUploadEventFile{
		ID:           task.ID,
		FileName:     task.FileName,
		OriginalName: task.OriginalFileName,
		URL:          "",
		Size:         task.Size,
		MimeType:     task.MimeType,
		Width:        task.GetWidth(),
		Height:       task.GetHeight(),
		IsPrimary:    task.IsPrimary,
	}

	return eventFileSendResizer
}
