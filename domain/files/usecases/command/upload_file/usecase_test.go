package uploadfile_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"path"
	"strings"
	"testing"
	"time"

	commonshared "github.com/assurrussa/goshared/pkg/filetypes"
	"github.com/assurrussa/goshared/pkg/logger"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/assurrussa/goshared/pkg/tests"
	eventstreammocks "github.com/assurrussa/goshared/services/event-stream/mocks"
	"github.com/google/uuid"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
	uploadfilemocks "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file/mocks"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

const (
	filePreset           = "main"
	downloadURL          = "https://resizer.example.com/my-bucket/uploads/admin/12/main/example.png"
	fileURL              = "https://ceph.example.com/my-bucket/uploads/admin/12/main/example.png"
	cephBaseURL          = "https://ceph.example.com/my-bucket"
	videoPresetMain      = "video_mp4_main"
	videoPresetThumbnail = "video_mp4_main_thumbnail"
	videoPresetPreview   = "video_mp4_main_preview"
	videoDownloadURL     = "https://resizer.example.com/my-bucket/uploads/admin/12/video/video.mp4"
	videoThumbnailURL    = "https://resizer.example.com/my-bucket/uploads/admin/12/video/video-thumb.webp"
	videoPreviewURL      = "https://resizer.example.com/my-bucket/uploads/admin/12/video/video-preview.webp"
	videoFileURL         = "https://ceph.example.com/my-bucket/uploads/admin/12/video/video.mp4"
)

type TestSuite struct {
	suite.Suite

	fileMock         *uploadfilemocks.MockfileRepository
	clientResizeMock *uploadfilemocks.MockresizeClient
	fileStorageMock  *uploadfilemocks.MockfileStorage
	transactorMock   *uploadfilemocks.Mocktransactor
	outboxMock       *uploadfilemocks.MockoutboxPutter
	eventStreamMock  *eventstreammocks.MockEventStream

	useCase       *uploadfile.UseCase
	expectedError error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		clientResizeMock := uploadfilemocks.NewMockresizeClient(ctrl)
		fileStorageMock := uploadfilemocks.NewMockfileStorage(ctrl)
		transactorMock := uploadfilemocks.NewMocktransactor(ctrl)
		fileMock := uploadfilemocks.NewMockfileRepository(ctrl)
		eventStreamMock := eventstreammocks.NewMockEventStream(ctrl)
		outboxMock := uploadfilemocks.NewMockoutboxPutter(ctrl)

		useCase := uploadfile.Must(uploadfile.NewOptions(
			transactorMock,
			fileMock,
			clientResizeMock,
			eventStreamMock,
			log,
			fileStorageMock,
			outboxMock,
		))

		return &TestSuite{
			useCase:          useCase,
			fileMock:         fileMock,
			clientResizeMock: clientResizeMock,
			transactorMock:   transactorMock,
			eventStreamMock:  eventStreamMock,
			outboxMock:       outboxMock,
			fileStorageMock:  fileStorageMock,
			expectedError:    errors.New("expected error"),
		}
	})
}

func TestHandle_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		uploadfile.Must(uploadfile.NewOptions(
			nil, nil, nil, nil, nil, nil, nil,
		))
	})
}

func TestHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	file := testshelpers.CreateFile(t)
	file.FileType = commonshared.FileTypeImage
	uploaderFile := file.GetData().Uploader
	file.URL = fileURL
	file.IsPrimary = true
	file.FileName = uuid.NewString() + ".png"
	readerOpenResp := testshelpers.CreateTestImage(t)
	clientResizerReq := clientresizer.RequestDownload{
		Preset:    filePreset,
		URL:       downloadURL,
		TypeMedia: file.FileType.ToString(),
	}
	body, err := io.ReadAll(readerOpenResp)
	ts.Require().NoError(err)
	file.Size = int64(len(body))
	clientResizerResp := clientresizer.ResponseDownload{
		Body: body,
	}

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)
	ts.fileStorageMock.EXPECT().Delete(ctx, file.GetFullPath()).Return(nil).Times(1)
	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientResizerReq).Return(clientResizerResp, nil).Times(1)
	var (
		storedFileName string
		storedFileURL  string
		storedRelPath  string
	)

	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			ts.Equal(file.FolderPath, input.Dir)
			ts.Equal(file.FileName, input.FileName)
			storedFileName = input.FileName
			storedRelPath = path.Join(file.FolderPath, input.FileName)
			storedFileURL = fmt.Sprintf("%s/%s", cephBaseURL, storedRelPath)

			return filestorage.StoredFile{
				RelativePath: storedRelPath,
				URL:          storedFileURL,
				Size:         file.Size,
				MimeType:     "image/png",
			}, nil
		}).Times(1)

	ts.fileMock.EXPECT().
		Update(ctx, file.ID, gomock.AssignableToTypeOf(model.File{})).
		DoAndReturn(func(_ context.Context, _ int64, updated model.File) error {
			ts.Equal(storedFileName, updated.FileName)
			ts.Equal(storedFileURL, updated.URL)
			ts.Equal(file.Size, updated.Size)
			ts.Equal("image/png", updated.MimeType)

			data := updated.GetData()
			ts.Empty(data.Uploader)
			ts.Contains(data.Presets, shared.PresetName(filePreset))

			mainPreset := data.Presets[shared.PresetName(filePreset)]
			ts.Equal(storedFileURL, mainPreset.URL)
			ts.Equal(storedRelPath, mainPreset.RelativePath)
			ts.Equal("image/png", mainPreset.MimeType)
			ts.Equal(file.Size, mainPreset.Size)

			return nil
		}).Times(1)

	ts.eventStreamMock.EXPECT().
		Publish(ctx, uploaderFile.UserUUID, gomock.AssignableToTypeOf(shared.FileUploadStatusEvent{})).
		DoAndReturn(func(_ context.Context, _ sharedtypes.UserID, event shared.FileUploadStatusEvent) error {
			ts.Equal(file.ID, event.File.ID)
			ts.Equal(storedFileName, event.File.FileName)
			ts.Equal(storedFileURL, event.File.URL)
			ts.Equal(shared.FileUploadTaskStatusCompleted, event.Status)

			return nil
		}).Times(1)

	ts.outboxMock.EXPECT().
		Put(gomock.Any(), "test_name_job", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, job, payload string, availableAt time.Time) (int64, error) {
			ts.Equal("test_name_job", job)
			ts.NotZero(availableAt)

			var pl map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &pl))
			ts.InDelta(float64(file.ID), pl["fileId"], 0.1)

			meta, ok := pl["meta"].(map[string]any)
			ts.Require().True(ok)
			ts.Equal(storedFileName, meta["fileName"])
			ts.Equal(storedFileURL, meta["fileUrl"])
			ts.Equal(file.OriginalFileName, meta["originalFileName"])

			return 777, nil
		}).Times(1)

	resp, err := ts.useCase.Handle(ctx, uploadfile.Request{
		FileID: file.ID,
		Artifacts: []uploadfile.Artifact{
			{
				Preset:      filePreset,
				URL:         downloadURL,
				MediaType:   "image",
				ContentType: "image/png",
				ExpireAt:    time.Now().Add(2 * time.Hour),
			},
			{
				Preset:      filePreset,
				URL:         downloadURL,
				MediaType:   "image",
				ContentType: "image/png",
				ExpireAt:    time.Now().Add(-(2 * time.Hour)),
			},
		},
	})
	ts.Require().NoError(err)
	ts.Empty(resp)
}

func TestHandle_VideoPresets(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	file := testshelpers.CreateFile(t)
	file.FileType = commonshared.FileTypeVideo
	file.MimeType = "video/mp4"
	file.FileName = uuid.NewString() + ".mov"
	file.URL = videoFileURL
	file.Size = 0
	baseSlug := strings.TrimSuffix(file.FileName, path.Ext(file.FileName))

	fileData := file.GetData()
	fileData.Width = 0
	fileData.Height = 0
	file.SetData(fileData)

	uploaderFile := file.GetData().Uploader

	mainBody := sampleMP4Header()
	thumbReader := testshelpers.CreateTestImage(t)
	thumbBody, err := io.ReadAll(thumbReader)
	ts.Require().NoError(err)

	previewReader := testshelpers.CreateTestImage(t)
	previewBody, err := io.ReadAll(previewReader)
	ts.Require().NoError(err)

	var (
		mainStoredName     string
		mainStoredRelPath  string
		mainStoredURL      string
		thumbStoredRelPath string
		thumbStoredURL     string
		previewStoredRel   string
		previewStoredURL   string
	)

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)
	ts.fileStorageMock.EXPECT().Delete(ctx, file.GetFullPath()).Return(nil).Times(1)

	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset:    videoPresetMain,
		URL:       videoDownloadURL,
		TypeMedia: file.FileType.ToString(),
	}).Return(clientresizer.ResponseDownload{
		Body: mainBody,
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			ts.Equal(file.FolderPath, input.Dir)
			ts.Equal(baseSlug, strings.TrimSuffix(input.FileName, path.Ext(input.FileName)))
			mainStoredName = input.FileName
			mainStoredRelPath = path.Join(file.FolderPath, input.FileName)
			mainStoredURL = fmt.Sprintf("%s/%s", cephBaseURL, mainStoredRelPath)

			return filestorage.StoredFile{
				RelativePath: mainStoredRelPath,
				URL:          mainStoredURL,
				Size:         int64(len(mainBody)),
				MimeType:     "video/mp4",
			}, nil
		}).Times(1)

	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset:    videoPresetThumbnail,
		URL:       videoThumbnailURL,
		TypeMedia: "image",
	}).Return(clientresizer.ResponseDownload{
		Body: thumbBody,
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			ts.Equal(path.Join(file.FolderPath, videoPresetThumbnail), input.Dir)
			ts.Equal(baseSlug, strings.TrimSuffix(input.FileName, path.Ext(input.FileName)))
			thumbStoredRelPath = path.Join(file.FolderPath, videoPresetThumbnail, input.FileName)
			thumbStoredURL = fmt.Sprintf("%s/%s", cephBaseURL, thumbStoredRelPath)

			return filestorage.StoredFile{
				RelativePath: thumbStoredRelPath,
				URL:          thumbStoredURL,
				Size:         int64(len(thumbBody)),
				MimeType:     "image/png",
			}, nil
		}).Times(1)

	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset:    videoPresetPreview,
		URL:       videoPreviewURL,
		TypeMedia: "image",
	}).Return(clientresizer.ResponseDownload{
		Body: previewBody,
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			ts.Equal(path.Join(file.FolderPath, videoPresetPreview), input.Dir)
			ts.Equal(baseSlug, strings.TrimSuffix(input.FileName, path.Ext(input.FileName)))
			previewStoredRel = path.Join(file.FolderPath, videoPresetPreview, input.FileName)
			previewStoredURL = fmt.Sprintf("%s/%s", cephBaseURL, previewStoredRel)

			return filestorage.StoredFile{
				RelativePath: previewStoredRel,
				URL:          previewStoredURL,
				Size:         int64(len(previewBody)),
				MimeType:     "image/png",
			}, nil
		}).Times(1)

	ts.fileMock.EXPECT().
		Update(ctx, file.ID, gomock.AssignableToTypeOf(model.File{})).
		DoAndReturn(func(_ context.Context, _ int64, updated model.File) error {
			ts.Equal(mainStoredName, updated.FileName)
			ts.Equal(mainStoredURL, updated.URL)
			ts.Equal(int64(len(mainBody)), updated.Size)
			ts.Equal("video/mp4", updated.MimeType)

			data := updated.GetData()
			ts.Equal(320, data.Width)
			ts.Equal(180, data.Height)
			ts.Empty(data.Uploader)

			mainPreset := data.Presets[shared.PresetName(videoPresetMain)]
			ts.Equal(mainStoredURL, mainPreset.URL)
			ts.Equal(mainStoredRelPath, mainPreset.RelativePath)
			ts.Equal(int64(len(mainBody)), mainPreset.Size)

			thumbPreset := data.Presets[shared.PresetName(videoPresetThumbnail)]
			ts.Equal(thumbStoredURL, thumbPreset.URL)
			ts.Equal(thumbStoredRelPath, thumbPreset.RelativePath)
			ts.True(thumbPreset.IsThumbnail)

			previewPreset := data.Presets[shared.PresetName(videoPresetPreview)]
			ts.Equal(previewStoredURL, previewPreset.URL)
			ts.Equal(previewStoredRel, previewPreset.RelativePath)
			ts.True(previewPreset.IsPreview)

			return nil
		}).Times(1)

	ts.eventStreamMock.EXPECT().
		Publish(ctx, uploaderFile.UserUUID, gomock.AssignableToTypeOf(shared.FileUploadStatusEvent{})).
		DoAndReturn(func(_ context.Context, _ sharedtypes.UserID, event shared.FileUploadStatusEvent) error {
			ts.Equal(file.ID, event.File.ID)
			ts.Equal(mainStoredURL, event.File.URL)
			ts.Equal(mainStoredName, event.File.FileName)

			return nil
		}).Times(1)

	ts.outboxMock.EXPECT().
		Put(gomock.Any(), "test_name_job", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, job, payload string, availableAt time.Time) (int64, error) {
			ts.Equal("test_name_job", job)
			ts.NotZero(availableAt)

			var pl map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &pl))
			ts.InDelta(float64(file.ID), pl["fileId"], 0.1)

			meta, ok := pl["meta"].(map[string]any)
			ts.Require().True(ok)
			ts.Equal(mainStoredName, meta["fileName"])
			ts.Equal(mainStoredURL, meta["fileUrl"])

			return 777, nil
		}).Times(1)

	resp, err := ts.useCase.Handle(ctx, uploadfile.Request{
		FileID: file.ID,
		Artifacts: []uploadfile.Artifact{
			{
				Preset:      videoPresetMain,
				URL:         videoDownloadURL,
				MediaType:   "video",
				ContentType: "video/mp4",
				ExpireAt:    time.Now().Add(2 * time.Hour),
				Metadata: map[string]any{
					"media_type":    "video",
					"target_width":  float64(320),
					"target_height": float64(180),
				},
			},
			{
				Preset:      videoPresetThumbnail,
				URL:         videoThumbnailURL,
				MediaType:   "image",
				ContentType: "image/png",
				ExpireAt:    time.Now().Add(2 * time.Hour),
				Metadata: map[string]any{
					"thumbnail": true,
				},
			},
			{
				Preset:      videoPresetPreview,
				URL:         videoPreviewURL,
				MediaType:   "image",
				ContentType: "image/png",
				ExpireAt:    time.Now().Add(2 * time.Hour),
				Metadata: map[string]any{
					"preview": true,
				},
			},
		},
	})
	ts.Require().NoError(err)
	ts.Empty(resp)
}

func sampleMP4Header() []byte {
	return []byte{
		0x00, 0x00, 0x00, 0x20,
		0x66, 0x74, 0x79, 0x70,
		0x69, 0x73, 0x6F, 0x6D,
		0x00, 0x00, 0x00, 0x00,
		0x69, 0x73, 0x6F, 0x6D,
		0x69, 0x73, 0x6F, 0x32,
		0x00, 0x00, 0x00, 0x08,
		0x66, 0x72, 0x65, 0x65,
		0x00, 0x00, 0x02, 0xD4,
		0x6D, 0x64, 0x61, 0x74,
		0x00, 0x00, 0x00, 0x00,
	}
}
