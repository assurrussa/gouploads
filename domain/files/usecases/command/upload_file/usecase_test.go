package uploadfile_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"path"
	"strings"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	commonshared "github.com/assurrussa/gouploads/domain/files/model"
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
	fileURL              = "https://s3store.example.com/my-bucket/uploads/admin/12/main/example.png"
	s3BaseURL            = "https://s3store.example.com/my-bucket"
	videoPresetMain      = "video_mp4_main"
	videoPresetThumbnail = "video_mp4_main_thumbnail"
	videoPresetPreview   = "video_mp4_main_preview"
	videoDownloadURL     = "https://resizer.example.com/my-bucket/uploads/admin/12/video/video.mp4"
	videoThumbnailURL    = "https://resizer.example.com/my-bucket/uploads/admin/12/video/video-thumb.webp"
	videoPreviewURL      = "https://resizer.example.com/my-bucket/uploads/admin/12/video/video-preview.webp"
	videoFileURL         = "https://s3store.example.com/my-bucket/uploads/admin/12/video/video.mp4"
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
			uploadfile.WithDeliveryBaseURL("https://media.example.test"),
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
		Body:          io.NopCloser(bytes.NewReader(body)),
		ContentLength: int64(len(body)),
	}

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)
	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientResizerReq).Return(clientResizerResp, nil).Times(1)
	var (
		storedFileName string
		storedRelPath  string
	)
	finalDir := path.Join("media/v1", file.ObjectType.String(), file.ObjectID.String(), file.Slug)

	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			ts.Equal(finalDir, input.Dir)
			ts.Equal("main.png", input.FileName)
			written, err := io.Copy(io.Discard, input.Reader)
			ts.Require().NoError(err)
			storedFileName = input.FileName
			storedRelPath = path.Join(input.Dir, input.FileName)

			return filestorage.StoredFile{
				RelativePath: storedRelPath,
				Size:         written,
				MimeType:     "image/png",
			}, nil
		}).Times(1)

	ts.fileMock.EXPECT().
		Update(ctx, file.ID, gomock.AssignableToTypeOf(model.File{})).
		DoAndReturn(func(_ context.Context, _ int64, updated model.File) error {
			ts.Equal(storedFileName, updated.FileName)
			ts.Empty(updated.URL)
			ts.Equal(file.Size, updated.Size)
			ts.Equal("image/png", updated.MimeType)

			data := updated.GetData()
			ts.Empty(data.Uploader)
			ts.Contains(data.Presets, shared.PresetName(filePreset))

			mainPreset := data.Presets[shared.PresetName(filePreset)]
			ts.Empty(mainPreset.URL)
			ts.Equal(storedRelPath, mainPreset.RelativePath)
			ts.Equal("image/png", mainPreset.MimeType)
			ts.Equal(file.Size, mainPreset.Size)
			ts.Len(mainPreset.ChecksumSHA256, 64)

			return nil
		}).Times(1)

	ts.eventStreamMock.EXPECT().
		Publish(ctx, uploaderFile.UserUUID, gomock.AssignableToTypeOf(shared.FileUploadStatusEvent{})).
		DoAndReturn(func(_ context.Context, _ sharedtypes.UserID, event shared.FileUploadStatusEvent) error {
			ts.Equal(file.ID, event.File.ID)
			ts.Equal(storedFileName, event.File.FileName)
			ts.Equal("https://media.example.test/"+storedRelPath, event.File.URL)
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
			ts.Equal("https://media.example.test/"+storedRelPath, meta["fileUrl"])
			ts.Equal(file.OriginalFileName, meta["originalFileName"])

			return 777, nil
		}).Times(1)

	ts.outboxMock.EXPECT().
		Put(gomock.Any(), "deleted_file", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, payload string, _ time.Time) (int64, error) {
			var data map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &data))
			ts.Equal(file.GetFullPath(), data["filepath"])
			return 778, nil
		}).Times(1)

	resp, err := ts.useCase.Handle(ctx, uploadfile.Request{
		FileID: file.ID,
		Artifacts: []uploadfile.Artifact{
			{
				Preset:      filePreset,
				URL:         downloadURL,
				MediaType:   "image",
				ContentType: "image/png",
				Size:        int64(len(body)),
				ExpireAt:    time.Now().Add(2 * time.Hour),
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
		thumbStoredRelPath string
		previewStoredRel   string
	)
	finalDir := path.Join("media/v1", file.ObjectType.String(), file.ObjectID.String(), file.Slug)

	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, fn func(ctx context.Context) error) error {
			return fn(ctx)
		}).Times(1)
	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)
	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset:    videoPresetMain,
		URL:       videoDownloadURL,
		TypeMedia: file.FileType.ToString(),
	}).Return(clientresizer.ResponseDownload{
		Body:          io.NopCloser(bytes.NewReader(mainBody)),
		ContentLength: int64(len(mainBody)),
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			ts.Equal(finalDir, input.Dir)
			ts.Equal(videoPresetMain, strings.TrimSuffix(input.FileName, path.Ext(input.FileName)))
			written, err := io.Copy(io.Discard, input.Reader)
			ts.Require().NoError(err)
			mainStoredName = input.FileName
			mainStoredRelPath = path.Join(input.Dir, input.FileName)

			return filestorage.StoredFile{
				RelativePath: mainStoredRelPath,
				Size:         written,
				MimeType:     "video/mp4",
			}, nil
		}).Times(1)

	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset:    videoPresetThumbnail,
		URL:       videoThumbnailURL,
		TypeMedia: "image",
	}).Return(clientresizer.ResponseDownload{
		Body:          io.NopCloser(bytes.NewReader(thumbBody)),
		ContentLength: int64(len(thumbBody)),
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			ts.Equal(finalDir, input.Dir)
			ts.Equal(videoPresetThumbnail, strings.TrimSuffix(input.FileName, path.Ext(input.FileName)))
			written, err := io.Copy(io.Discard, input.Reader)
			ts.Require().NoError(err)
			thumbStoredRelPath = path.Join(input.Dir, input.FileName)

			return filestorage.StoredFile{
				RelativePath: thumbStoredRelPath,
				Size:         written,
				MimeType:     "image/png",
			}, nil
		}).Times(1)

	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset:    videoPresetPreview,
		URL:       videoPreviewURL,
		TypeMedia: "image",
	}).Return(clientresizer.ResponseDownload{
		Body:          io.NopCloser(bytes.NewReader(previewBody)),
		ContentLength: int64(len(previewBody)),
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			ts.Equal(finalDir, input.Dir)
			ts.Equal(videoPresetPreview, strings.TrimSuffix(input.FileName, path.Ext(input.FileName)))
			written, err := io.Copy(io.Discard, input.Reader)
			ts.Require().NoError(err)
			previewStoredRel = path.Join(input.Dir, input.FileName)

			return filestorage.StoredFile{
				RelativePath: previewStoredRel,
				Size:         written,
				MimeType:     "image/png",
			}, nil
		}).Times(1)

	ts.fileMock.EXPECT().
		Update(ctx, file.ID, gomock.AssignableToTypeOf(model.File{})).
		DoAndReturn(func(_ context.Context, _ int64, updated model.File) error {
			ts.Equal(mainStoredName, updated.FileName)
			ts.Empty(updated.URL)
			ts.Equal(int64(len(mainBody)), updated.Size)
			ts.Equal("video/mp4", updated.MimeType)

			data := updated.GetData()
			ts.Equal(320, data.Width)
			ts.Equal(180, data.Height)
			ts.Empty(data.Uploader)

			mainPreset := data.Presets[shared.PresetName(videoPresetMain)]
			ts.Empty(mainPreset.URL)
			ts.Equal(mainStoredRelPath, mainPreset.RelativePath)
			ts.Equal(int64(len(mainBody)), mainPreset.Size)
			ts.Len(mainPreset.ChecksumSHA256, 64)

			thumbPreset := data.Presets[shared.PresetName(videoPresetThumbnail)]
			ts.Empty(thumbPreset.URL)
			ts.Equal(thumbStoredRelPath, thumbPreset.RelativePath)
			ts.True(thumbPreset.IsThumbnail)

			previewPreset := data.Presets[shared.PresetName(videoPresetPreview)]
			ts.Empty(previewPreset.URL)
			ts.Equal(previewStoredRel, previewPreset.RelativePath)
			ts.True(previewPreset.IsPreview)

			return nil
		}).Times(1)

	ts.eventStreamMock.EXPECT().
		Publish(ctx, uploaderFile.UserUUID, gomock.AssignableToTypeOf(shared.FileUploadStatusEvent{})).
		DoAndReturn(func(_ context.Context, _ sharedtypes.UserID, event shared.FileUploadStatusEvent) error {
			ts.Equal(file.ID, event.File.ID)
			ts.Equal("https://media.example.test/"+mainStoredRelPath, event.File.URL)
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
			ts.Equal("https://media.example.test/"+mainStoredRelPath, meta["fileUrl"])

			return 777, nil
		}).Times(1)

	ts.outboxMock.EXPECT().
		Put(gomock.Any(), "deleted_file", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, payload string, _ time.Time) (int64, error) {
			var data map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &data))
			ts.Equal(file.GetFullPath(), data["filepath"])
			return 778, nil
		}).Times(1)

	resp, err := ts.useCase.Handle(ctx, uploadfile.Request{
		FileID: file.ID,
		Artifacts: []uploadfile.Artifact{
			{
				Preset:      videoPresetMain,
				URL:         videoDownloadURL,
				MediaType:   "video",
				ContentType: "video/mp4",
				Size:        int64(len(mainBody)),
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
				Size:        int64(len(thumbBody)),
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
				Size:        int64(len(previewBody)),
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

func TestHandle_PartialArtifactFailureSchedulesFinalCleanup(t *testing.T) {
	ctx, cancel, ts := NewTestSuite(t)

	file := testshelpers.CreateFile(t)
	file.FileType = commonshared.FileTypeImage
	body, err := io.ReadAll(testshelpers.CreateTestImage(t))
	ts.Require().NoError(err)
	mainPath := path.Join("media/v1", file.ObjectType.String(), file.ObjectID.String(), file.Slug, "main.png")

	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)
	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset: "main", URL: downloadURL, TypeMedia: "image",
	}).Return(clientresizer.ResponseDownload{
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)),
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			written, copyErr := io.Copy(io.Discard, input.Reader)
			ts.Require().NoError(copyErr)
			return filestorage.StoredFile{
				RelativePath: path.Join(input.Dir, input.FileName),
				Size:         written,
				MimeType:     "image/png",
			}, nil
		}).Times(1)
	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset: "thumbnail", URL: "https://resizer.example.com/thumbnail.png", TypeMedia: "image",
	}).Return(clientresizer.ResponseDownload{}, ts.expectedError).Times(1)
	ts.outboxMock.EXPECT().
		Put(gomock.Any(), "deleted_file", gomock.Any(), gomock.Any()).
		DoAndReturn(func(cleanupCtx context.Context, _ string, payload string, _ time.Time) (int64, error) {
			ts.Require().NoError(cleanupCtx.Err(), "terminal cleanup must outlive the failed attempt context")
			var data map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &data))
			ts.Equal(mainPath, data["filepath"])
			return 901, nil
		}).Times(1)

	cancel()
	_, err = ts.useCase.Handle(ctx, uploadfile.Request{
		FileID:           file.ID,
		CleanupOnFailure: true,
		Artifacts: []uploadfile.Artifact{
			{
				Preset: "main", URL: downloadURL, MediaType: "image", ContentType: "image/png",
				Size: int64(len(body)), ExpireAt: time.Now().Add(time.Hour),
			},
			{
				Preset: "thumbnail", URL: "https://resizer.example.com/thumbnail.png", MediaType: "image",
				ContentType: "image/png", ExpireAt: time.Now().Add(time.Hour),
			},
		},
	})
	ts.Require().ErrorIs(err, ts.expectedError)
}

func TestHandle_CompletedRecordWithClearedUploaderIsIdempotent(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	file := testshelpers.CreateFile(t)
	data := file.GetData()
	data.Uploader = shared.FileUploader{}
	data.Presets = map[shared.PresetName]shared.FilePreset{
		shared.FilePresetMainName: {
			PresetName:   shared.FilePresetMainName.String(),
			RelativePath: "media/v1/admin/42/file-slug/main.png",
		},
	}
	file.SetData(data)
	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)

	resp, err := ts.useCase.Handle(ctx, uploadfile.Request{
		FileID: file.ID,
		Artifacts: []uploadfile.Artifact{{
			Preset:      "main",
			URL:         downloadURL,
			MediaType:   "image",
			ContentType: "image/png",
			Size:        1,
			ExpireAt:    time.Now().Add(time.Hour),
		}},
	})
	ts.Require().NoError(err)
	ts.Empty(resp)
}

func TestHandle_PartialArtifactFailureKeepsFinalsForRetry(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	file := testshelpers.CreateFile(t)
	file.FileType = commonshared.FileTypeImage
	body, err := io.ReadAll(testshelpers.CreateTestImage(t))
	ts.Require().NoError(err)

	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)
	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset: "main", URL: downloadURL, TypeMedia: "image",
	}).Return(clientresizer.ResponseDownload{
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)),
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			written, copyErr := io.Copy(io.Discard, input.Reader)
			ts.Require().NoError(copyErr)
			return filestorage.StoredFile{
				RelativePath: path.Join(input.Dir, input.FileName),
				Size:         written,
				MimeType:     "image/png",
			}, nil
		}).Times(1)
	ts.clientResizeMock.EXPECT().DownloadFile(ctx, clientresizer.RequestDownload{
		Preset: "thumbnail", URL: "https://resizer.example.com/thumbnail.png", TypeMedia: "image",
	}).Return(clientresizer.ResponseDownload{}, ts.expectedError).Times(1)

	_, err = ts.useCase.Handle(ctx, uploadfile.Request{
		FileID: file.ID,
		Artifacts: []uploadfile.Artifact{
			{
				Preset: "main", URL: downloadURL, MediaType: "image", ContentType: "image/png",
				Size: int64(len(body)), ExpireAt: time.Now().Add(time.Hour),
			},
			{
				Preset: "thumbnail", URL: "https://resizer.example.com/thumbnail.png", MediaType: "image",
				ContentType: "image/png", ExpireAt: time.Now().Add(time.Hour),
			},
		},
	})
	ts.Require().ErrorIs(err, ts.expectedError)
}

func TestHandle_ExpiredArtifactFailsWithoutCompleting(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	file := testshelpers.CreateFile(t)
	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)

	_, err := ts.useCase.Handle(ctx, uploadfile.Request{
		FileID: file.ID,
		Artifacts: []uploadfile.Artifact{{
			Preset:      "main",
			URL:         downloadURL,
			MediaType:   "image",
			ContentType: "image/png",
			ExpireAt:    time.Now().Add(-time.Minute),
		}},
	})
	ts.Require().ErrorContains(err, "artifact \"main\" is expired")
}

func TestHandle_RejectsFinalizationWithoutMainArtifact(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	file := testshelpers.CreateFile(t)
	file.FileType = commonshared.FileTypeImage
	body, err := io.ReadAll(testshelpers.CreateTestImage(t))
	ts.Require().NoError(err)

	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)
	ts.clientResizeMock.EXPECT().DownloadFile(ctx, gomock.Any()).Return(clientresizer.ResponseDownload{
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)),
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().SavePersist(ctx, gomock.Any()).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			written, copyErr := io.Copy(io.Discard, input.Reader)
			ts.Require().NoError(copyErr)
			return filestorage.StoredFile{
				RelativePath: path.Join(input.Dir, input.FileName),
				Size:         written,
				MimeType:     "image/png",
			}, nil
		}).Times(1)

	_, err = ts.useCase.Handle(ctx, uploadfile.Request{
		FileID: file.ID,
		Artifacts: []uploadfile.Artifact{{
			Preset:      "thumbnail",
			URL:         downloadURL,
			MediaType:   "image",
			ContentType: "image/png",
			Size:        int64(len(body)),
			ExpireAt:    time.Now().Add(time.Hour),
			Metadata:    map[string]any{"thumbnail": true},
		}},
	})
	ts.Require().ErrorContains(err, "finalization has no main artifact")
}

func TestHandle_TransactionFailureSchedulesFinalCleanup(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	file := testshelpers.CreateFile(t)
	file.FileType = commonshared.FileTypeImage
	body, err := io.ReadAll(testshelpers.CreateTestImage(t))
	ts.Require().NoError(err)
	mainPath := path.Join("media/v1", file.ObjectType.String(), file.ObjectID.String(), file.Slug, "main.png")

	ts.fileMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil).Times(1)
	ts.clientResizeMock.EXPECT().DownloadFile(ctx, gomock.Any()).Return(clientresizer.ResponseDownload{
		Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)),
	}, nil).Times(1)
	ts.fileStorageMock.EXPECT().
		SavePersist(ctx, gomock.AssignableToTypeOf(filestorage.SaveFileInput{})).
		DoAndReturn(func(_ context.Context, input filestorage.SaveFileInput) (filestorage.StoredFile, error) {
			written, copyErr := io.Copy(io.Discard, input.Reader)
			ts.Require().NoError(copyErr)
			return filestorage.StoredFile{
				RelativePath: path.Join(input.Dir, input.FileName),
				Size:         written,
				MimeType:     "image/png",
			}, nil
		}).Times(1)
	ts.transactorMock.EXPECT().RunInTx(gomock.Any(), gomock.Any()).Return(ts.expectedError).Times(1)
	ts.outboxMock.EXPECT().
		Put(gomock.Any(), "deleted_file", gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ string, payload string, _ time.Time) (int64, error) {
			var data map[string]any
			ts.Require().NoError(json.Unmarshal([]byte(payload), &data))
			ts.Equal(mainPath, data["filepath"])
			return 902, nil
		}).Times(1)

	_, err = ts.useCase.Handle(ctx, uploadfile.Request{
		FileID:           file.ID,
		CleanupOnFailure: true,
		Artifacts: []uploadfile.Artifact{{
			Preset: "main", URL: downloadURL, MediaType: "image", ContentType: "image/png",
			Size: int64(len(body)), ExpireAt: time.Now().Add(time.Hour),
		}},
	})
	ts.Require().ErrorIs(err, ts.expectedError)
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
