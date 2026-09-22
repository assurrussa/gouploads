package sendresizefile_test

import (
	"context"
	"errors"
	"testing"

	logger "github.com/assurrussa/gologger"
	commonshared "github.com/assurrussa/goshared/pkg/filetypes"
	"github.com/assurrussa/goshared/pkg/tests"
	eventstreammocks "github.com/assurrussa/goshared/services/event-stream/mocks"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	testsmatcher "github.com/assurrussa/gouploads/domain/files/tests/matcher"
	sendresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
	sendresizefilemocks "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file/mocks"
)

const (
	sourceURL = "https://resizer.example.com/src/tmp/image.jpg"
)

type TestSuite struct {
	suite.Suite

	fileRepositoryMock *sendresizefilemocks.MockfileRepository
	resizeClientMock   *sendresizefilemocks.MockresizeClient
	sourceResolverMock *sendresizefilemocks.MocksourceURLResolver
	eventStreamMock    *eventstreammocks.MockEventStream

	imagePipeline config.ImagePipelineConfig
	videoPipeline config.VideoPipelineConfig

	useCase       *sendresizefile.UseCase
	expectedError error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		fileRepositoryMock := sendresizefilemocks.NewMockfileRepository(ctrl)
		resizeClientMock := sendresizefilemocks.NewMockresizeClient(ctrl)
		sourceResolverMock := sendresizefilemocks.NewMocksourceURLResolver(ctrl)
		eventStreamMock := eventstreammocks.NewMockEventStream(ctrl)
		sourceResolverMock.EXPECT().
			Resolve(gomock.Any(), gomock.Any()).
			DoAndReturn(func(_ context.Context, source string) (string, error) { return source, nil }).
			AnyTimes()
		videoPipeline := config.VideoPipelineConfig{
			ResizerHost:         "https://resizer.example.com/images/jobs",
			WebhookCallbackHost: "https://webhook.example.com/hook/images/resizer",
			Presets: []config.VideoPresetConfig{
				{
					Name:    "small",
					Width:   320,
					Height:  320,
					Format:  "mp4",
					Quality: 82,
					Thumbnail: config.PresetPreviewConfig{
						Enabled:   true,
						Timestamp: 1,
						Width:     160,
						Height:    90,
						Format:    "webp",
					},
					Preview: config.PresetPreviewConfig{
						Enabled: true,
						Width:   320,
						Height:  180,
						Format:  "jpg",
					},
				},
			},
		}
		imagePipeline := config.ImagePipelineConfig{
			DefaultFormat:       "jpg",
			ResizerHost:         "https://resizer.example.com/videos/jobs",
			WebhookCallbackHost: "https://webhook.example.com/hook/videos/resizer",
			Presets: []config.ImagePresetConfig{
				{
					Name:    "thumbnail_jpg",
					Width:   320,
					Height:  320,
					Format:  "jpg",
					Quality: 82,
					Fit:     "cover",
				},
				{
					Name:    "thumbnail_webp",
					Width:   320,
					Height:  320,
					Format:  "webp",
					Quality: 82,
					Fit:     "cover",
				},
			},
			Watermark: config.ImageWatermarkConfig{
				Enabled:  true,
				Image:    "https://example.com/image.jpg",
				Opacity:  0.4,
				Position: "center",
				OffsetX:  0,
				OffsetY:  0,
				Scale:    0,
			},
		}

		useCase := sendresizefile.Must(sendresizefile.NewOptions(
			fileRepositoryMock,
			resizeClientMock,
			sourceResolverMock,
			eventStreamMock,
			imagePipeline,
			videoPipeline,
			log,
		))

		return &TestSuite{
			useCase:            useCase,
			fileRepositoryMock: fileRepositoryMock,
			resizeClientMock:   resizeClientMock,
			sourceResolverMock: sourceResolverMock,
			eventStreamMock:    eventStreamMock,
			imagePipeline:      imagePipeline,
			videoPipeline:      videoPipeline,
			expectedError:      errors.New("expected error"),
		}
	})
}

func TestHandle_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		sendresizefile.Must(sendresizefile.NewOptions(
			nil, nil, nil, nil,
			config.ImagePipelineConfig{}, config.VideoPipelineConfig{}, nil,
		))
	})
}

func TestHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	jobID := outboxtypes.MustParse[outboxtypes.JobID]("f0317e88-bbfe-11ed-8728-461e464ebed8")
	fileModel := testshelpers.CreateFile(t)
	respClient := clientresizer.Response{
		JobID:  jobID,
		Status: "queued",
	}
	eventFileSendResizer := createEvent(fileModel, respClient, shared.FileUploadTaskStatusProcessing)
	//nolint:lll // tests
	dataSend := `{"idempotency_key":"123456","type":"image","skip_resize":false,"notify_webhook_url":"https://webhook.example.com/hook/videos/resizer","metadata":{"fileId":"123456"},"presets":[{"format":"jpg","height":320,"name":"thumbnail_jpg","target":"image","fit":"cover","width":320,"quality":82,"video_bitrate":0,"audio_bitrate":0},{"format":"webp","height":320,"name":"thumbnail_webp","target":"image","fit":"cover","width":320,"quality":82,"video_bitrate":0,"audio_bitrate":0}],"source":{"url":"https://resizer.example.com/src/tmp/image.jpg"}}`

	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, nil).Times(1)
	ts.resizeClientMock.EXPECT().SendResize(ctx, clientresizer.Request{
		TypeMedia: "image",
		Data:      []byte(dataSend),
	}).Return(respClient, nil).Times(1)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, fileModel.GetData().Uploader.UserUUID, testsmatcher.NewEventPublishMatcher(
			"file publish create matcher", eventFileSendResizer,
		)).
		Return(nil).Times(1)

	resp, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FilePath:        sourceURL,
		FileID:          fileModel.ID,
		SkipResizeVideo: true,
	})
	ts.Require().NoError(err)
	ts.Equal(respClient.JobID, resp.JobID)
	ts.Equal(respClient.Status, resp.Status)
}

func TestHandle_SuccessSkipResizeVideo(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	jobID := outboxtypes.MustParse[outboxtypes.JobID]("f0317e88-bbfe-11ed-8728-461e464ebed8")
	fileModel := testshelpers.CreateFile(t)
	fileModel.FileType = commonshared.FileTypeVideo
	fileModel.MimeType = "video/mp4"
	respClient := clientresizer.Response{
		JobID:  jobID,
		Status: "queued",
	}
	eventFileSendResizer := createEvent(fileModel, respClient, shared.FileUploadTaskStatusProcessing)
	//nolint:lll // tests
	dataSend := `{"idempotency_key":"123456","type":"video","skip_resize":true,"notify_webhook_url":"https://webhook.example.com/hook/images/resizer","metadata":{"fileId":"123456"},"presets":[{"format":"mp4","height":320,"name":"small","target":"video","fit":"","width":320,"quality":82,"video_bitrate":0,"audio_bitrate":0,"thumbnail":{"enabled":true,"timestamp":1,"width":160,"height":90,"format":"webp"},"preview":{"enabled":true,"width":320,"height":180,"format":"jpg"}}],"source":{"url":"https://resizer.example.com/src/tmp/image.jpg"}}`

	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, nil).Times(1)
	ts.resizeClientMock.EXPECT().SendResize(ctx, clientresizer.Request{
		TypeMedia: "video",
		Data:      []byte(dataSend),
	}).Return(respClient, nil).Times(1)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, fileModel.GetData().Uploader.UserUUID, testsmatcher.NewEventPublishMatcher(
			"file publish create matcher", eventFileSendResizer,
		)).
		Return(nil).Times(1)

	resp, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FilePath:        sourceURL,
		FileID:          fileModel.ID,
		SkipResizeVideo: true,
	})
	ts.Require().NoError(err)
	ts.Equal(respClient.JobID, resp.JobID)
	ts.Equal(respClient.Status, resp.Status)
}

func TestHandle_SuccessVideo(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	jobID := outboxtypes.MustParse[outboxtypes.JobID]("f0317e88-bbfe-11ed-8728-461e464ebed8")
	fileModel := testshelpers.CreateFile(t)
	fileModel.MimeType = "video/mp4"
	respClient := clientresizer.Response{
		JobID:  jobID,
		Status: "queued",
	}
	eventFileSendResizer := createEvent(fileModel, respClient, shared.FileUploadTaskStatusProcessing)

	var capturedRequest clientresizer.Request
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, nil).Times(1)
	ts.resizeClientMock.EXPECT().
		SendResize(ctx, gomock.Any()).
		DoAndReturn(func(_ context.Context, req clientresizer.Request) (clientresizer.Response, error) {
			capturedRequest = req
			return respClient, nil
		}).Times(1)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, fileModel.GetData().Uploader.UserUUID, testsmatcher.NewEventPublishMatcher(
			"file publish create matcher", eventFileSendResizer,
		)).
		Return(nil).Times(1)

	resp, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FilePath: sourceURL,
		FileID:   fileModel.ID,
	})
	ts.Require().NoError(err)
	ts.Equal(respClient.JobID, resp.JobID)
	ts.Equal(respClient.Status, resp.Status)

	payload, err := sendresizefile.UnmarshalPayload(string(capturedRequest.Data))
	ts.Require().NoError(err)
	ts.Len(payload.Presets, len(ts.videoPipeline.Presets))
	videoPreset := payload.Presets[0]
	ts.NotNil(videoPreset.Thumbnail)
	ts.True(videoPreset.Thumbnail.Enabled)
	ts.Equal(160, videoPreset.Thumbnail.Width)
	ts.Equal(90, videoPreset.Thumbnail.Height)
	ts.Equal("webp", videoPreset.Thumbnail.Format)
	ts.NotNil(videoPreset.Preview)
	ts.True(videoPreset.Preview.Enabled)
	ts.Equal(320, videoPreset.Preview.Width)
	ts.Equal(180, videoPreset.Preview.Height)
	ts.Equal("jpg", videoPreset.Preview.Format)
}

func TestHandle_ErrorPublish(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	jobID := outboxtypes.MustParse[outboxtypes.JobID]("f0317e88-bbfe-11ed-8728-461e464ebed8")
	fileModel := testshelpers.CreateFile(t)
	respClient := clientresizer.Response{
		JobID:  jobID,
		Status: "queued",
	}
	eventFileSendResizer := createEvent(fileModel, respClient, shared.FileUploadTaskStatusProcessing)

	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, nil).Times(1)
	ts.resizeClientMock.EXPECT().SendResize(ctx, gomock.Any()).Return(respClient, nil).Times(1)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, fileModel.GetData().Uploader.UserUUID, testsmatcher.NewEventPublishMatcher(
			"file publish create matcher", eventFileSendResizer,
		)).
		Return(ts.expectedError).Times(1)

	resp, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FilePath: sourceURL,
		FileID:   fileModel.ID,
	})
	ts.Require().NoError(err)
	ts.Equal(respClient.JobID, resp.JobID)
	ts.Equal(respClient.Status, resp.Status)
}

func TestHandle_ErrorSendResize(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileModel := testshelpers.CreateFile(t)
	respClient := clientresizer.Response{}
	eventFileSendResizer := createEvent(fileModel, respClient, shared.FileUploadTaskStatusFailed)

	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, nil).Times(1)
	ts.resizeClientMock.EXPECT().SendResize(ctx, gomock.Any()).Return(respClient, ts.expectedError).Times(1)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, fileModel.GetData().Uploader.UserUUID, testsmatcher.NewEventPublishMatcher(
			"file publish send resize matcher", eventFileSendResizer,
		)).
		Return(nil).Times(1)

	resp, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FilePath: sourceURL,
		FileID:   fileModel.ID,
	})
	ts.Require().ErrorIs(err, ts.expectedError)
	ts.Equal(respClient.JobID, resp.JobID)
	ts.Equal(respClient.Status, resp.Status)
}

func TestHandle_ErrorPayloadType(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileModel := testshelpers.CreateFile(t)
	fileModel.MimeType = "unknown/type"
	respClient := clientresizer.Response{}
	eventFileSendResizer := createEvent(fileModel, respClient, shared.FileUploadTaskStatusFailed)

	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, nil).Times(1)
	ts.eventStreamMock.EXPECT().
		Publish(ctx, fileModel.GetData().Uploader.UserUUID, testsmatcher.NewEventPublishMatcher(
			"file publish send resize matcher", eventFileSendResizer,
		)).
		Return(nil).Times(1)

	resp, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FilePath: sourceURL,
		FileID:   fileModel.ID,
	})
	ts.Require().Error(err)
	ts.Equal(respClient.JobID, resp.JobID)
	ts.Equal(respClient.Status, resp.Status)
}

func TestHandle_TaskCompleted(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileModel := testshelpers.CreateFile(t)
	fileModel.Data.Uploader.Status = shared.FileUploadTaskStatusCompleted

	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, nil).Times(1)

	resp, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FilePath: sourceURL,
		FileID:   fileModel.ID,
	})
	ts.Require().NoError(err)
	ts.Empty(resp)
}

func TestHandle_ErrorGetByID(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	fileModel := testshelpers.CreateFile(t)

	ts.fileRepositoryMock.EXPECT().GetByID(ctx, fileModel.ID).Return(fileModel, ts.expectedError).Times(1)

	resp, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FilePath: sourceURL,
		FileID:   fileModel.ID,
	})
	ts.Require().ErrorIs(err, ts.expectedError)
	ts.Empty(resp)
}

func TestHandle_Empty(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	resp, err := ts.useCase.Handle(ctx, sendresizefile.Request{})
	ts.Require().Error(err)
	ts.Empty(resp)
}

func createEvent(
	fileModel model.File,
	respClient clientresizer.Response,
	status shared.FileUploadTaskStatus,
) shared.FileUploadStatusEvent {
	eventFileSendResizer := shared.NewFileUploadStatusEvent(fileModel.ID, status)
	eventFileSendResizer.Metadata = map[string]any{
		"jobId":        respClient.JobID,
		"jobStatus":    respClient.Status,
		"uploaderUuid": fileModel.GetData().Uploader.UserUUID.String(),
		"objectType":   fileModel.ObjectType.String(),
		"objectId":     fileModel.ObjectID.String(),
	}
	eventFileSendResizer.File = &shared.FileUploadEventFile{
		ID:           fileModel.ID,
		FileName:     fileModel.FileName,
		OriginalName: fileModel.OriginalFileName,
		URL:          "",
		Size:         fileModel.Size,
		MimeType:     fileModel.MimeType,
		Width:        fileModel.GetWidth(),
		Height:       fileModel.GetHeight(),
		IsPrimary:    fileModel.IsPrimary,
	}
	return eventFileSendResizer
}
