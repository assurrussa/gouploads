package uploadservice_test

import (
	"context"
	"errors"
	"mime/multipart"
	"testing"

	"github.com/assurrussa/goshared/pkg/logger"
	"github.com/assurrussa/goshared/pkg/pointer"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/assurrussa/goshared/pkg/tests"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	deletedfile "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	sendresizefilejob "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	uploadservicemocks "github.com/assurrussa/gouploads/domain/files/service/uploadservice/mocks"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	testsmatcher "github.com/assurrussa/gouploads/domain/files/tests/matcher"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

const (
	fileName = "example.png"
)

type TestSuite struct {
	suite.Suite

	ctrl               *gomock.Controller
	mockFileRepository *uploadservicemocks.MockfileRepository
	mockOutboxPutter   *uploadservicemocks.MockoutboxPutter
	mockTransactor     *uploadservicemocks.Mocktransactor
	mockFileStorage    *uploadservicemocks.MockfileStorage

	svc *uploadservice.Service
}

func NewTestRepoSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()
	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockFileRepository := uploadservicemocks.NewMockfileRepository(ctrl)
		mockOutboxPutter := uploadservicemocks.NewMockoutboxPutter(ctrl)
		mockTransactor := uploadservicemocks.NewMocktransactor(ctrl)
		mockFileStorage := uploadservicemocks.NewMockfileStorage(ctrl)

		svc := uploadservice.Must(uploadservice.NewOptions(
			mockTransactor,
			mockOutboxPutter,
			mockFileRepository,
			logger.Discard(),
			mockFileStorage,
		))

		return &TestSuite{
			ctrl:               ctrl,
			mockFileRepository: mockFileRepository,
			mockOutboxPutter:   mockOutboxPutter,
			mockTransactor:     mockTransactor,
			mockFileStorage:    mockFileStorage,
			svc:                svc,
		}
	})
}

func Test_Init(t *testing.T) {
	assert.Panics(t, func() {
		uploadservice.Must(uploadservice.NewOptions(
			nil,
			nil,
			nil,
			nil,
			nil,
		))
	})
}

func TestService_UploadBatch_Success(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileHeader := testshelpers.MakeFileHeaderImage(t, "photo", fileName, "image/png")
	fileModel := testshelpers.CreateFile(t)
	fileModel.OriginalFileName = fileName
	fileID := fileModel.ID
	fileModel.ID = 0
	fileModel.Size = 80

	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
	body, err := fileHeader.Open()
	ts.Require().NoError(err)

	input := filestorage.SaveFileInput{
		Dir:      "uploads/admin/12",
		FileName: fileModel.OriginalFileName,
		Size:     fileModel.Size,
		MimeType: fileModel.MimeType,
		Reader:   body,
	}
	ts.mockFileStorage.EXPECT().SaveTemp(ctx, testsmatcher.NewS3Matcher("s3 upload batch 1", input)).
		Return(filestorage.StoredFile{
			RelativePath: "uploads/admin/12/example.png",
			URL:          fileModel.URL,
			Size:         fileModel.Size,
			MimeType:     fileModel.MimeType,
		}, nil).Times(1)

	ts.mockFileRepository.EXPECT().
		Create(gomock.Any(), testsmatcher.NewFileShortMatcher("upload batch success", fileModel)).
		Return(fileID, nil).Times(1)

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		fileID,
		"uploads/admin/12/example.png",
		false,
	))
	ts.Require().NoError(err)
	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), sendresizefilejob.JobName, payload, gomock.Any()).Times(1)

	uuid := fileModel.GetData().Uploader.UserUUID
	req := uploadservice.BatchRequest{
		FileHeaders:  []*multipart.FileHeader{fileHeader},
		UploaderUUID: uuid,
		ManagerID:    *fileModel.ManagerID,
		UserID:       *fileModel.UserID,
		ObjectType:   fileModel.ObjectType,
		ObjectID:     *fileModel.ObjectID,
		AfterJobs: shared.NewFileEventAfterJobs(
			"model_avatar_bind", uuid, map[string]any{"adminId": uuid},
		),
		Config: &uploadservice.FileUploadConfig{
			AllowedExtensions: []string{".png"},
			AllowedMimeTypes: map[string][]string{
				".png": {"image/png"},
				".txt": {"text/plain"},
			},
		},
	}
	files, err := ts.svc.UploadBatch(ctx, req)
	ts.Require().NoError(err)
	ts.Len(files, 1)
	ts.Equal(fileID, files[0].ID)
	ts.Equal("queued", files[0].GetData().Uploader.Status.String())
	ts.NotEmpty(files[0].URL)
	ts.Contains(files[0].FileName, ".png")
	ts.Contains(files[0].URL, "/uploads/admin/12")
}

func TestService_UploadBatch_RelativeSourceKey(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileHeader := testshelpers.MakeFileHeaderImage(t, "photo", fileName, "image/png")
	fileModel := testshelpers.CreateFile(t)
	fileModel.OriginalFileName = fileName
	fileID := fileModel.ID
	fileModel.ID = 0
	fileModel.Size = 80

	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
	body, err := fileHeader.Open()
	ts.Require().NoError(err)

	input := filestorage.SaveFileInput{
		Dir:      "uploads/admin/12",
		FileName: fileModel.OriginalFileName,
		Size:     fileModel.Size,
		MimeType: fileModel.MimeType,
		Reader:   body,
	}
	ts.mockFileStorage.EXPECT().SaveTemp(ctx, testsmatcher.NewS3Matcher("s3 upload batch internal host", input)).
		Return(filestorage.StoredFile{
			RelativePath: "uploads/admin/12/example.png",
			URL:          fileModel.URL,
			Size:         fileModel.Size,
			MimeType:     fileModel.MimeType,
		}, nil).Times(1)

	ts.mockFileRepository.EXPECT().
		Create(gomock.Any(), testsmatcher.NewFileShortMatcher("upload batch internal host", fileModel)).
		Return(fileID, nil).Times(1)

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		fileID,
		"uploads/admin/12/example.png",
		false,
	))
	ts.Require().NoError(err)
	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), sendresizefilejob.JobName, payload, gomock.Any()).Times(1)

	req := uploadservice.BatchRequest{
		FileHeaders:  []*multipart.FileHeader{fileHeader},
		UploaderUUID: fileModel.GetData().Uploader.UserUUID,
		ManagerID:    *fileModel.ManagerID,
		UserID:       *fileModel.UserID,
		ObjectType:   fileModel.ObjectType,
		ObjectID:     *fileModel.ObjectID,
	}

	files, err := ts.svc.UploadBatch(ctx, req)
	ts.Require().NoError(err)
	ts.Len(files, 1)
	ts.Equal(fileID, files[0].ID)
	ts.Equal("queued", files[0].GetData().Uploader.Status.String())
	ts.Equal(fileModel.URL, files[0].URL)
}

func TestService_UploadBatch_DefaultConfigApplied(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileHeader := testshelpers.MakeFileHeaderImage(t, "photos", "cover.png", "image/png")
	fileModel := testshelpers.CreateFile(t)
	fileModel.OriginalFileName = "cover.png"
	fileID := fileModel.ID
	fileModel.ID = 0
	fileModel.Size = 80
	fileModel.ObjectType = shared.ObjectTypeExercise
	fileModel.ObjectID = nil
	fileModel.ManagerID = pointer.To(int64(1))
	fileModel.UserID = pointer.To(int64(3))

	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
	body, err := fileHeader.Open()
	ts.Require().NoError(err)

	input := filestorage.SaveFileInput{
		Dir:      "uploads/admin/12",
		FileName: fileModel.OriginalFileName,
		Size:     fileModel.Size,
		MimeType: fileModel.MimeType,
		Reader:   body,
	}
	ts.mockFileStorage.EXPECT().SaveTemp(ctx, testsmatcher.NewS3Matcher("s3 upload batch 2", input)).
		Return(filestorage.StoredFile{
			RelativePath: "uploads/admin/12/cover.png",
			URL:          fileModel.URL,
			Size:         fileModel.Size,
			MimeType:     fileModel.MimeType,
		}, nil).Times(1)

	ts.mockFileRepository.EXPECT().
		Create(gomock.Any(), testsmatcher.NewFileShortMatcher("upload batch default config", fileModel)).
		Return(fileID, nil).Times(1)

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		fileID,
		"uploads/admin/12/cover.png",
		false,
	))
	ts.Require().NoError(err)
	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), sendresizefilejob.JobName, payload, gomock.Any()).Times(1)

	uuid := fileModel.GetData().Uploader.UserUUID
	req := uploadservice.BatchRequest{
		FileHeaders:  []*multipart.FileHeader{fileHeader},
		UploaderUUID: fileModel.GetData().Uploader.UserUUID,
		ManagerID:    *fileModel.ManagerID,
		UserID:       *fileModel.UserID,
		ObjectType:   fileModel.ObjectType,
		AfterJobs: shared.NewFileEventAfterJobs(
			"model_avatar_bind", uuid, map[string]any{"adminId": uuid},
		),
	}
	files, err := ts.svc.UploadBatch(ctx, req)
	ts.Require().NoError(err)
	ts.Len(files, 1)
	ts.Equal(fileID, files[0].ID)
	ts.Equal("queued", files[0].GetData().Uploader.Status.String())
	ts.NotEmpty(files[0].URL)
	ts.Contains(files[0].FileName, ".png")
	ts.Contains(files[0].URL, "/uploads/admin/12")
}

func TestService_UploadBatch_ValidationError(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	testID := sharedtypes.NewUserID()
	fileHeader := testshelpers.MakeFileHeader(t, "photos", "cover.txt", "batch content", "text/plain")

	req := uploadservice.BatchRequest{
		FileHeaders:  []*multipart.FileHeader{fileHeader},
		UploaderUUID: testID,
		ManagerID:    1,
		UserID:       3,
		ObjectType:   shared.ObjectTypeExercise,
		AfterJobs:    shared.NewFileEventAfterJobs("model_avatar_bind", testID, map[string]any{"testId": 1}),
		Config: &uploadservice.FileUploadConfig{
			AllowedExtensions: []string{".png"},
		},
	}
	tasks, err := ts.svc.UploadBatch(ctx, req)
	ts.Require().Error(err)
	ts.Require().Nil(tasks)
	var valErr uploadservice.ClientError
	ts.Require().ErrorAs(err, &valErr)

	// No transactional operations should be performed on validation failure.
}

func TestService_UploadBatch_NoFiles(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	req := uploadservice.BatchRequest{
		FileHeaders: []*multipart.FileHeader{},
		ManagerID:   1,
		UserID:      3,
		ObjectType:  shared.ObjectTypeExercise,
		ObjectID:    shared.FileObjectID(12344),
		DeletedID:   shared.FileObjectID(12343),
		Config: &uploadservice.FileUploadConfig{
			AllowedExtensions: []string{".txt"},
			AllowedMimeTypes: map[string][]string{
				".txt": {"text/plain"},
			},
		},
	}
	tasks, err := ts.svc.UploadBatch(ctx, req)
	ts.Require().ErrorIs(err, uploadservice.ErrNoFiles)
	ts.Nil(tasks)
}

func TestService_UploadBatch_RunInTxError(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileHeader := testshelpers.MakeFileHeader(t, "photos", "cover.txt", "batch content", "text/plain")
	fileModel := testshelpers.CreateFile(t)

	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).Return(assert.AnError)
	ts.mockFileStorage.EXPECT().SaveTemp(ctx, gomock.Any()).
		Return(filestorage.StoredFile{
			RelativePath: "uploads/admin/12/example.png",
			URL:          fileModel.URL,
			Size:         fileModel.Size,
			MimeType:     fileModel.MimeType,
		}, nil).Times(1)

	req := uploadservice.BatchRequest{
		FileHeaders:  []*multipart.FileHeader{fileHeader},
		UploaderUUID: fileModel.GetData().Uploader.UserUUID,
		ManagerID:    1,
		UserID:       3,
		ObjectType:   shared.ObjectTypeExercise,
		ObjectID:     shared.FileObjectID(12344),
		Config: &uploadservice.FileUploadConfig{
			AllowedExtensions: []string{".txt"},
			AllowedMimeTypes: map[string][]string{
				".txt": {"text/plain"},
			},
		},
	}
	_, err := ts.svc.UploadBatch(ctx, req)
	ts.Require().ErrorIs(err, assert.AnError)
}

func TestService_UploadSingle_Success(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileHeader := testshelpers.MakeFileHeaderImage(t, "photo", fileName, "image/png")
	fileModel := testshelpers.CreateFile(t)
	fileModel.OriginalFileName = fileName
	fileID := fileModel.ID
	fileModel.ID = 0
	fileModel.Size = 80
	replacedFile := testshelpers.CreateFile(t)
	replacedFile.ID = 12343
	replacedFile.ObjectType = fileModel.ObjectType
	replacedFile.ObjectID = pointer.To(*fileModel.ObjectID)
	ts.mockFileRepository.EXPECT().GetByID(ctx, replacedFile.ID).Return(replacedFile, nil).Times(1)

	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
	body, err := fileHeader.Open()
	ts.Require().NoError(err)

	input := filestorage.SaveFileInput{
		Dir:      "uploads/admin/12",
		FileName: fileModel.OriginalFileName,
		Size:     fileModel.Size,
		MimeType: fileModel.MimeType,
		Reader:   body,
	}
	ts.mockFileStorage.EXPECT().SaveTemp(ctx, testsmatcher.NewS3Matcher("s3 upload single", input)).
		Return(filestorage.StoredFile{
			RelativePath: "uploads/admin/12/example.png",
			URL:          fileModel.URL,
			Size:         fileModel.Size,
			MimeType:     fileModel.MimeType,
		}, nil).Times(1)

	ts.mockFileRepository.EXPECT().
		Create(gomock.Any(), testsmatcher.NewFileShortMatcher("upload single success", fileModel)).
		DoAndReturn(func(_ context.Context, created model.File) (int64, error) {
			afterJobs := created.GetData().Uploader.AfterJobs
			ts.Require().Len(afterJobs, 2)
			payload, payloadErr := deletedfile.UnmarshalPayload(afterJobs[1].Payload)
			ts.Require().NoError(payloadErr)
			ts.Equal(replacedFile.ID, payload.FileID)
			ts.Equal(fileModel.ObjectType, payload.ObjectType)
			ts.Equal(*fileModel.ObjectID, payload.ObjectID)
			return fileID, nil
		}).Times(1)

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		fileID,
		"uploads/admin/12/example.png",
		false,
	))
	ts.Require().NoError(err)
	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), sendresizefilejob.JobName, payload, gomock.Any()).Times(1)

	uuid := fileModel.GetData().Uploader.UserUUID
	req := uploadservice.SingleRequest{
		FileHeader:   fileHeader,
		UploaderUUID: uuid,
		ManagerID:    *fileModel.ManagerID,
		UserID:       *fileModel.UserID,
		ObjectType:   fileModel.ObjectType,
		ObjectID:     *fileModel.ObjectID,
		DeletedID:    shared.FileObjectID(12343),
		AfterJobs: shared.NewFileEventAfterJobs(
			"model_avatar_bind", uuid, map[string]any{"adminId": uuid},
		),
		Config: &uploadservice.FileUploadConfig{
			AllowedExtensions: []string{".png"},
			AllowedMimeTypes: map[string][]string{
				".png": {"image/png"},
				".txt": {"text/plain"},
			},
		},
	}
	file, err := ts.svc.UploadSingle(ctx, req)
	ts.Require().NoError(err)
	ts.Require().NotNil(file)
	ts.Equal(fileID, file.ID)
	ts.Equal("queued", file.GetData().Uploader.Status.String())
	ts.NotEmpty(file.URL)
	ts.Contains(file.FileName, ".png")
	ts.Contains(file.URL, "/uploads/admin/12")
}

func TestService_UploadSingle_SkipResizer(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileHeader := testshelpers.MakeFileHeaderImage(t, "photo", fileName, "image/png")
	fileModel := testshelpers.CreateFile(t)
	fileModel.OriginalFileName = fileName
	fileID := fileModel.ID
	fileModel.ID = 0
	fileModel.Size = 80

	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
	body, err := fileHeader.Open()
	ts.Require().NoError(err)

	input := filestorage.SaveFileInput{
		Dir:      "uploads/admin/12",
		FileName: fileModel.OriginalFileName,
		Size:     fileModel.Size,
		MimeType: fileModel.MimeType,
		Reader:   body,
	}
	ts.mockFileStorage.EXPECT().SaveTemp(ctx, testsmatcher.NewS3Matcher("s3 upload skip resizer", input)).
		Return(filestorage.StoredFile{
			RelativePath: "uploads/admin/12/example.png",
			URL:          fileModel.URL,
			Size:         fileModel.Size,
			MimeType:     fileModel.MimeType,
		}, nil).Times(1)

	ts.mockFileRepository.EXPECT().
		Create(gomock.Any(), testsmatcher.NewFileShortMatcher("upload single skip resizer", fileModel)).
		Return(fileID, nil).Times(1)

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		fileID,
		"uploads/admin/12/example.png",
		true,
	))
	ts.Require().NoError(err)
	ts.mockOutboxPutter.EXPECT().
		Put(gomock.Any(), sendresizefilejob.JobName, payload, gomock.Any()).
		Return(outboxtypes.NewJobID(), nil).Times(1)

	uuid := fileModel.GetData().Uploader.UserUUID
	req := uploadservice.SingleRequest{
		FileHeader:   fileHeader,
		UploaderUUID: uuid,
		ManagerID:    *fileModel.ManagerID,
		UserID:       *fileModel.UserID,
		ObjectType:   fileModel.ObjectType,
		ObjectID:     *fileModel.ObjectID,
		AfterJobs: shared.NewFileEventAfterJobs(
			"model_avatar_bind", uuid, map[string]any{"adminId": uuid},
		),
		Config: &uploadservice.FileUploadConfig{
			AllowedExtensions: []string{".png"},
			AllowedMimeTypes: map[string][]string{
				".png": {"image/png"},
			},
			SkipResizer: true,
		},
	}

	file, err := ts.svc.UploadSingle(ctx, req)
	ts.Require().NoError(err)
	ts.Equal(fileID, file.ID)
	ts.Equal("queued", file.GetData().Uploader.Status.String())
}

func TestService_UploadSingle_ClientError(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileHeader := testshelpers.MakeFileHeader(t, "avatar", "avatar.txt", "single content", "text/plain")

	req := uploadservice.SingleRequest{
		FileHeader:   fileHeader,
		UploaderUUID: sharedtypes.NewUserID(),
		ManagerID:    1,
		UserID:       3,
		ObjectType:   shared.ObjectTypeExercise,
		ObjectID:     shared.FileObjectID(12344),
		Config: &uploadservice.FileUploadConfig{
			AllowedExtensions: []string{".png"},
			AllowedMimeTypes: map[string][]string{
				".png": {"image/png"},
			},
		},
	}
	_, err := ts.svc.UploadSingle(ctx, req)
	ts.Require().Error(err)
	var clientErr uploadservice.ClientError
	ts.Require().ErrorAs(err, &clientErr)
}

func TestService_UploadStoredRejectsReplacementFromDifferentObject(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	const deletedID = int64(17)
	replaced := testshelpers.CreateFile(t)
	replaced.ID = deletedID
	replaced.ObjectType = shared.ObjectTypeAdmin
	replaced.ObjectID = pointer.To(shared.FileObjectID(99))
	ts.mockFileRepository.EXPECT().GetByID(ctx, deletedID).Return(replaced, nil).Times(1)

	_, err := ts.svc.UploadStored(ctx, uploadservice.ReaderRequest{
		UploaderUUID: sharedtypes.NewUserID(),
		ManagerID:    3,
		ObjectType:   shared.ObjectTypeAdmin,
		ObjectID:     shared.FileObjectID(3),
		DeletedID:    shared.FileObjectID(deletedID),
	}, uploadservice.UploadedFile{
		OriginalName: "client.webp",
		FileName:     "source.webp",
		Path:         "staging/v1/tus/session/source.webp",
		FolderPath:   "staging/v1/tus/session",
		MimeType:     "image/webp",
	})
	ts.Require().Error(err)
	var clientErr uploadservice.ClientError
	ts.Require().ErrorAs(err, &clientErr)
	ts.Equal("replacement file does not belong to the upload object", clientErr.Message)
}

func TestService_UploadSingle_EnqueueError(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileHeader := testshelpers.MakeFileHeaderImage(t, "photo", fileName, "image/png")
	fileModel := testshelpers.CreateFile(t)
	fileModel.OriginalFileName = fileName
	fileModel.ID = 0
	fileModel.Size = 80

	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
	body, err := fileHeader.Open()
	ts.Require().NoError(err)

	input := filestorage.SaveFileInput{
		Dir:      "uploads/admin/12",
		FileName: fileModel.OriginalFileName,
		Size:     fileModel.Size,
		MimeType: fileModel.MimeType,
		Reader:   body,
	}
	ts.mockFileStorage.EXPECT().SaveTemp(ctx, testsmatcher.NewS3Matcher("s3 upload single enqueue error", input)).
		Return(filestorage.StoredFile{
			RelativePath: "uploads/admin/12/example.png",
			URL:          fileModel.URL,
			Size:         fileModel.Size,
			MimeType:     fileModel.MimeType,
		}, nil).Times(1)

	ts.mockFileRepository.EXPECT().
		Create(gomock.Any(), testsmatcher.NewFileShortMatcher("upload single enqueue error", fileModel)).
		Return(int64(0), assert.AnError).Times(1)

	uuid := fileModel.GetData().Uploader.UserUUID
	req := uploadservice.SingleRequest{
		FileHeader:   fileHeader,
		UploaderUUID: fileModel.GetData().Uploader.UserUUID,
		ManagerID:    *fileModel.ManagerID,
		UserID:       *fileModel.UserID,
		ObjectType:   fileModel.ObjectType,
		ObjectID:     *fileModel.ObjectID,
		AfterJobs: shared.NewFileEventAfterJobs(
			"model_avatar_bind", uuid, map[string]any{"adminId": uuid},
		),
		Config: &uploadservice.FileUploadConfig{
			AllowedExtensions: []string{".png"},
			AllowedMimeTypes: map[string][]string{
				".png": {"image/png"},
				".txt": {"text/plain"},
			},
		},
	}
	_, err = ts.svc.UploadSingle(ctx, req)
	ts.Require().Error(err)
	ts.Require().Contains(err.Error(), "create upload task")
}

func TestService_UploadSingle_OutboxError(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileHeader := testshelpers.MakeFileHeaderImage(t, "photo", fileName, "image/png")
	fileModel := testshelpers.CreateFile(t)
	fileModel.OriginalFileName = fileName
	fileID := fileModel.ID
	fileModel.ID = 0
	fileModel.Size = 80

	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).
		DoAndReturn(func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		})
	body, err := fileHeader.Open()
	ts.Require().NoError(err)

	input := filestorage.SaveFileInput{
		Dir:      "uploads/admin/12",
		FileName: fileModel.OriginalFileName,
		Size:     fileModel.Size,
		MimeType: fileModel.MimeType,
		Reader:   body,
	}
	ts.mockFileStorage.EXPECT().SaveTemp(ctx, testsmatcher.NewS3Matcher("s3 upload single outbox error", input)).
		Return(filestorage.StoredFile{
			RelativePath: "uploads/admin/12/example.png",
			URL:          fileModel.URL,
			Size:         fileModel.Size,
			MimeType:     fileModel.MimeType,
		}, nil).Times(1)

	ts.mockFileRepository.EXPECT().
		Create(gomock.Any(), testsmatcher.NewFileShortMatcher("upload single outbox error", fileModel)).
		Return(fileID, nil).Times(1)

	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(
		fileID,
		"uploads/admin/12/example.png",
		false,
	))
	ts.Require().NoError(err)
	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), sendresizefilejob.JobName, payload, gomock.Any()).
		Return(outboxtypes.JobIDNil, assert.AnError).Times(1)

	uuid := fileModel.GetData().Uploader.UserUUID
	req := uploadservice.SingleRequest{
		FileHeader:   fileHeader,
		UploaderUUID: fileModel.GetData().Uploader.UserUUID,
		ManagerID:    *fileModel.ManagerID,
		UserID:       *fileModel.UserID,
		ObjectType:   fileModel.ObjectType,
		ObjectID:     *fileModel.ObjectID,
		AfterJobs: shared.NewFileEventAfterJobs(
			"model_avatar_bind", uuid, map[string]any{"adminId": uuid},
		),
		Config: &uploadservice.FileUploadConfig{
			AllowedExtensions: []string{".png"},
			AllowedMimeTypes: map[string][]string{
				".png": {"image/png"},
				".txt": {"text/plain"},
			},
		},
	}
	_, err = ts.svc.UploadSingle(ctx, req)
	ts.Require().Error(err)
	ts.Require().Contains(err.Error(), "put outbox job")
}

func TestService_DeleteFile_Success(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	fileID := int64(123)
	userID := sharedtypes.NewUserID()
	file := model.File{
		ID:               fileID,
		FileName:         "photo.png",
		OriginalFileName: "photo.png",
		URL:              "/upload_file/photo.png",
		MimeType:         "image/png",
		Size:             512,
		Data:             &model.FileData{Width: 100, Height: 200},
	}
	ts.mockFileRepository.EXPECT().GetByID(ctx, fileID).
		Return(file, nil).Times(1)

	payload, err := deletedfile.MarshalPayload(deletedfile.NewPayload(fileID, userID, file.GetFullPath()))
	ts.Require().NoError(err)

	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), deletedfile.JobName, payload, gomock.Any()).Times(1)

	err = ts.svc.DeleteFile(ctx, uploadservice.DeleteRequest{
		FileID:        fileID,
		UserRequestID: userID,
	})
	ts.Require().NoError(err)
}

func TestService_DeleteFile_AfterJobs(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	fileID := int64(321)
	userID := sharedtypes.NewUserID()
	objectID := shared.FileObjectID(77)
	file := model.File{
		ID:               fileID,
		FileName:         "avatar.png",
		OriginalFileName: "avatar.png",
		URL:              "/upload_file/admin/avatar.png",
		MimeType:         "image/png",
		Size:             1024,
		ObjectType:       shared.ObjectTypeAdmin,
		ObjectID:         pointer.To(objectID),
	}

	ts.mockFileRepository.EXPECT().GetByID(ctx, fileID).
		Return(file, nil).Times(1)

	eventPayloads := shared.NewFileEventAfterJobs("test_job_name", userID, map[string]any{
		"objectId": objectID,
	})
	deletedPayload, err := deletedfile.MarshalPayload(deletedfile.NewPayload(
		fileID, userID, file.GetFullPath(), eventPayloads...,
	))
	ts.Require().NoError(err)

	gomock.InOrder(
		ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), deletedfile.JobName, deletedPayload, gomock.Any()).Times(1),
	)

	err = ts.svc.DeleteFile(ctx, uploadservice.DeleteRequest{
		FileID:        fileID,
		UserRequestID: userID,
		AfterJobs:     eventPayloads,
	})
	ts.Require().NoError(err)
}

func TestService_DeleteFile_ErrorPut(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)

	errExpect := errors.New("error expected")

	fileID := int64(123)
	userID := sharedtypes.NewUserID()
	file := model.File{
		ID:               fileID,
		FileName:         "photo.png",
		OriginalFileName: "photo.png",
		URL:              "/upload_file/photo.png",
		MimeType:         "image/png",
		Size:             512,
		Data:             &model.FileData{Width: 100, Height: 200},
	}
	ts.mockFileRepository.EXPECT().GetByID(ctx, fileID).
		Return(file, nil).Times(1)

	payload, err := deletedfile.MarshalPayload(deletedfile.NewPayload(fileID, userID, file.GetFullPath()))
	ts.Require().NoError(err)

	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), deletedfile.JobName, payload, gomock.Any()).
		Return(outboxtypes.JobIDNil, errExpect).Times(1)

	err = ts.svc.DeleteFile(ctx, uploadservice.DeleteRequest{
		FileID:        fileID,
		UserRequestID: userID,
	})
	ts.Require().ErrorIs(err, errExpect)
}

func TestService_DeleteFile_ErrorGetByID(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)

	errExpect := errors.New("error expected")

	fileID := int64(123)
	userID := sharedtypes.NewUserID()
	ts.mockFileRepository.EXPECT().GetByID(ctx, fileID).Return(model.File{}, errExpect).Times(1)

	err := ts.svc.DeleteFile(ctx, uploadservice.DeleteRequest{
		FileID:        fileID,
		UserRequestID: userID,
	})
	ts.Require().ErrorIs(err, errExpect)
}

func TestService_DeleteFile_ErrorGetByIDFileIDEmpty(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)

	fileID := int64(123)
	userID := sharedtypes.NewUserID()
	ts.mockFileRepository.EXPECT().GetByID(ctx, fileID).Return(model.File{ID: 0}, nil).Times(1)

	err := ts.svc.DeleteFile(ctx, uploadservice.DeleteRequest{
		FileID:        fileID,
		UserRequestID: userID,
	})
	ts.Require().NoError(err)
}

func TestService_DeleteFile_ErrorReq(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)

	fileID := int64(123)
	userID := sharedtypes.NewUserID()

	err := ts.svc.DeleteFile(ctx, uploadservice.DeleteRequest{
		FileID:        -1,
		UserRequestID: userID,
	})
	ts.Require().Error(err)

	err = ts.svc.DeleteFile(ctx, uploadservice.DeleteRequest{
		FileID:        0,
		UserRequestID: userID,
	})
	ts.Require().Error(err)

	err = ts.svc.DeleteFile(ctx, uploadservice.DeleteRequest{
		FileID:        fileID,
		UserRequestID: sharedtypes.UserIDNil,
	})
	ts.Require().Error(err)
}

func TestService_SetPrimary_Success(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileID := int64(77)
	objectID := int64(901)
	fileModel := model.File{
		ID:         fileID,
		ObjectType: shared.ObjectTypeAdmin,
		ObjectID:   pointer.To(shared.FileObjectID(33)),
		FileName:   "avatar.png",
	}

	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error {
			return fn(ctx)
		},
	)

	ts.mockFileRepository.EXPECT().GetByID(gomock.Any(), fileID).Return(fileModel, nil)
	ts.mockFileRepository.EXPECT().
		ClearPrimary(gomock.Any(), shared.ObjectTypeExercise.String(), objectID, fileID).
		Return(nil)

	ts.mockFileRepository.EXPECT().
		SetPrimary(gomock.Any(), fileID, shared.ObjectTypeExercise.String(), objectID).
		Return(nil)

	err := ts.svc.SetPrimary(ctx, uploadservice.SetPrimaryRequest{
		FileID:     fileID,
		ObjectType: shared.ObjectTypeExercise,
		ObjectID:   objectID,
	})
	ts.Require().NoError(err)
}

func TestService_SetPrimary_FileNotFound(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()

	fileID := int64(88)
	objectID := int64(55)

	ts.mockFileRepository.EXPECT().GetByID(gomock.Any(), fileID).Return(model.File{}, nil)

	err := ts.svc.SetPrimary(ctx, uploadservice.SetPrimaryRequest{
		FileID:     fileID,
		ObjectType: shared.ObjectTypeExercise,
		ObjectID:   objectID,
	})
	ts.Require().ErrorIs(err, uploadservice.ErrFileNotFound)
}
