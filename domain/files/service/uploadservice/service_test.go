package uploadservice_test

import (
	"context"
	"errors"
	"io"
	"mime/multipart"
	"path"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	deletedfile "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	sendresize "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	uploadservicemocks "github.com/assurrussa/gouploads/domain/files/service/uploadservice/mocks"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/identity"
	"github.com/assurrussa/gouploads/internal/pointer"
	tests "github.com/assurrussa/gouploads/internal/testsupport"
)

const fileName = "example.png"

type TestSuite struct {
	suite.Suite
	ctrl               *gomock.Controller
	mockFileRepository *uploadservicemocks.MockfileRepository
	mockOutboxPutter   *uploadservicemocks.MockoutboxPutter
	mockTransactor     *uploadservicemocks.Mocktransactor
	mockFileStorage    *uploadservicemocks.MockfileStorage
	svc                *uploadservice.Service
}

func NewTestRepoSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()
	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()
		ctrl := gomock.NewController(t)
		repo := uploadservicemocks.NewMockfileRepository(ctrl)
		outbox := uploadservicemocks.NewMockoutboxPutter(ctrl)
		tx := uploadservicemocks.NewMocktransactor(ctrl)
		storage := uploadservicemocks.NewMockfileStorage(ctrl)
		svc := uploadservice.Must(uploadservice.NewOptions(tx, outbox, repo, logger.Discard(), storage))
		return &TestSuite{
			ctrl:               ctrl,
			mockFileRepository: repo,
			mockOutboxPutter:   outbox,
			mockTransactor:     tx,
			mockFileStorage:    storage,
			svc:                svc,
		}
	})
}

func Test_Init(t *testing.T) {
	require.Panics(t, func() { uploadservice.Must(uploadservice.NewOptions(nil, nil, nil, nil, nil)) })
}

func uploadRequest(t *testing.T) uploadservice.SingleRequest {
	t.Helper()
	return uploadservice.SingleRequest{
		UploaderUUID: identity.NewUserID(),
		ManagerID:    12,
		UserID:       3,
		FileHeader: testshelpers.MakeFileHeaderImage(t,
			"photo",
			fileName,
			"image/png"),
		ObjectType: shared.ObjectTypeAdmin,
		ObjectID:   12,
	}
}

func executeTx(ts *TestSuite) {
	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(),
		gomock.Any()).DoAndReturn(func(ctx context.Context,
		fn func(context.Context) error,
	) error {
		return fn(ctx)
	})
}

func stageUpload(t *testing.T, ts *TestSuite, source *string) {
	t.Helper()
	ts.mockFileStorage.EXPECT().SaveTemp(gomock.Any(),
		gomock.Any()).DoAndReturn(func(_ context.Context,
		input filestorage.SaveFileInput) (filestorage.StoredFile,
		error,
	) {
		body, err := io.ReadAll(input.Reader)
		require.NoError(t, err)
		*source = path.Join(input.Dir, input.FileName)
		return filestorage.StoredFile{RelativePath: *source, Size: int64(len(body)), MimeType: input.MimeType, URL: "/" + *source}, nil
	})
}

func TestServiceMediaUploadContracts(t *testing.T) {
	for _, batch := range []bool{false, true} {
		for _, skip := range []bool{false, true} {
			t.Run(string(rune('a'+btoi(batch)*2+btoi(skip))), func(t *testing.T) {
				ctx, cancel, ts := NewTestRepoSuite(t)
				defer cancel()
				req := uploadRequest(t)
				req.Config = &uploadservice.FileUploadConfig{SkipResizer: skip}
				req.AfterJobs = shared.NewFileEventAfterJobs("avatar_bind", req.UploaderUUID, map[string]any{"object": 12})
				var source string
				stageUpload(t, ts, &source)
				executeTx(ts)
				ts.mockFileRepository.EXPECT().Create(gomock.Any(),
					gomock.Any()).DoAndReturn(func(_ context.Context,
					file model.File) (int64,
					error,
				) {
					require.Equal(t, req.ObjectType, file.ObjectType)
					require.Equal(t, req.ObjectID, *file.ObjectID)
					require.Equal(t, req.ManagerID, *file.ManagerID)
					require.Equal(t, model.FileTypeImage, file.FileType)
					require.Equal(t, shared.FileUploadTaskStatusQueued, file.GetData().Uploader.Status)
					require.Equal(t, req.AfterJobs, file.GetData().Uploader.AfterJobs)
					require.Equal(t, source, file.GetFullPath())
					return int64(21), nil
				})
				ts.mockOutboxPutter.EXPECT().Put(gomock.Any(),
					sendresize.JobName,
					gomock.Any(),
					gomock.Any()).DoAndReturn(func(_ context.Context,
					_ string,
					payload string,
					_ time.Time) (outboxtypes.JobID,
					error,
				) {
					expected, err := sendresize.MarshalPayload(sendresize.NewPayload(21, source, skip))
					require.NoError(t, err)
					require.JSONEq(t, expected, payload)
					return outboxtypes.NewJobID(), nil
				})
				var file model.File
				var err error
				if batch {
					var files []model.File
					files,
						err = ts.svc.UploadBatch(ctx,
						uploadservice.BatchRequest{
							UploaderUUID: req.UploaderUUID,
							ManagerID:    req.ManagerID,
							UserID:       req.UserID,
							FileHeaders:  []*multipart.FileHeader{req.FileHeader},
							ObjectType:   req.ObjectType,
							ObjectID:     req.ObjectID,
							AfterJobs:    req.AfterJobs,
							Config:       req.Config,
						})
					require.NoError(t, err)
					require.Len(t, files, 1)
					file = files[0]
				} else {
					file, err = ts.svc.UploadSingle(ctx, req)
				}
				require.NoError(t, err)
				require.Equal(t, int64(21), file.ID)
				require.Equal(t, fileName, file.OriginalFileName)
				require.Equal(t, source, file.GetFullPath())
			})
		}
	}
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}

func TestServiceReplacementOwnershipAndAfterJobs(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()
	req := uploadRequest(t)
	req.DeletedID = 9
	req.AfterJobs = shared.NewFileEventAfterJobs("bind", req.UploaderUUID)
	old := model.File{ID: 9, ObjectType: req.ObjectType, ObjectID: pointer.To(req.ObjectID)}
	ts.mockFileRepository.EXPECT().GetByID(ctx, int64(9)).Return(old, nil)
	var source string
	stageUpload(t, ts, &source)
	executeTx(ts)
	ts.mockFileRepository.EXPECT().Create(gomock.Any(),
		gomock.Any()).DoAndReturn(func(_ context.Context,
		file model.File) (int64,
		error,
	) {
		jobs := file.GetData().Uploader.AfterJobs
		require.Len(t, jobs, 2)
		pl, err := deletedfile.UnmarshalPayload(jobs[1].Payload)
		require.NoError(t, err)
		require.Equal(t, int64(9), pl.FileID)
		require.Equal(t, req.ObjectType, pl.ObjectType)
		require.Equal(t, req.ObjectID, pl.ObjectID)
		return int64(21), nil
	})
	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(),
		sendresize.JobName,
		gomock.Any(),
		gomock.Any()).Return(outboxtypes.NewJobID(),
		nil)
	_, err := ts.svc.UploadSingle(ctx, req)
	require.NoError(t, err)
	require.Len(t, req.AfterJobs, 1, "request-owned slice must not be mutated")
}

func TestService_UploadStoredRejectsReplacementFromDifferentObject(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()
	ts.mockFileRepository.EXPECT().GetByID(ctx,
		int64(17)).Return(model.File{
		ID:         17,
		ObjectType: shared.ObjectTypeAdmin,
		ObjectID:   pointer.To(shared.FileObjectID(99)),
	},
		nil)
	_, err := ts.svc.UploadStored(ctx,
		uploadservice.ReaderRequest{
			UploaderUUID: identity.NewUserID(),
			ManagerID:    3,
			ObjectType:   shared.ObjectTypeAdmin,
			ObjectID:     3,
			DeletedID:    17,
		},
		uploadservice.UploadedFile{
			OriginalName: "source.png",
			FileName:     "source.png",
			Path:         "tmp/uploads/source.png",
			MimeType:     "image/png",
		})
	var clientErr uploadservice.ClientError
	require.ErrorAs(t, err, &clientErr)
	require.Equal(t, "replacement file does not belong to the upload object", clientErr.Message)
}

func TestServiceUploadFailures(t *testing.T) {
	failure := errors.New("injected failure")
	for _, phase := range []string{"validation", "transaction", "create", "outbox"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			req := uploadRequest(t)
			switch phase {
			case "validation":
				req.FileHeader = testshelpers.MakeFileHeader(t, "photo", "denied.txt", "text", "text/plain")
			case "transaction":
				var source string
				stageUpload(t, ts, &source)
				ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).Return(failure)
			case "create", "outbox":
				var source string
				stageUpload(t, ts, &source)
				executeTx(ts)
				if phase == "create" {
					ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(0), failure)
				} else {
					ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(21), nil)
					ts.mockOutboxPutter.EXPECT().Put(gomock.Any(),
						sendresize.JobName,
						gomock.Any(),
						gomock.Any()).Return(outboxtypes.JobIDNil,
						failure)
				}
			}
			_, err := ts.svc.UploadSingle(ctx, req)
			if phase == "validation" {
				var clientErr uploadservice.ClientError
				require.ErrorAs(t, err, &clientErr)
			} else {
				require.ErrorIs(t, err, failure)
			}
			// No storage.Delete expectation: an uncertain commit must retain its source.
		})
	}
}

func TestServiceBatchPartialResults(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()
	req := uploadRequest(t)
	var source string
	stageUpload(t, ts, &source)
	executeTx(ts)
	ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(21), nil)
	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(),
		sendresize.JobName,
		gomock.Any(),
		gomock.Any()).Return(outboxtypes.NewJobID(),
		nil)
	denied := testshelpers.MakeFileHeader(t, "photo", "denied.txt", "text", "text/plain")
	files, err := ts.svc.UploadBatch(ctx,
		uploadservice.BatchRequest{
			UploaderUUID: req.UploaderUUID,
			ManagerID:    req.ManagerID,
			ObjectType:   req.ObjectType,
			ObjectID:     req.ObjectID,
			FileHeaders: []*multipart.FileHeader{
				req.FileHeader,
				denied,
			},
		})
	require.Len(t, files, 1)
	require.Equal(t, int64(21), files[0].ID)
	var batchErr *uploadservice.BatchError
	require.ErrorAs(t, err, &batchErr)
	require.Equal(t, 1, batchErr.FailedIndex)
	var clientErr uploadservice.ClientError
	require.ErrorAs(t, err, &clientErr)
	files, err = ts.svc.UploadBatch(ctx, uploadservice.BatchRequest{})
	require.Nil(t, files)
	require.ErrorIs(t, err, uploadservice.ErrNoFiles)
}

func TestServiceDeleteContracts(t *testing.T) {
	failure := errors.New("injected failure")
	for _, phase := range []string{
		"success",
		"afterjobs",
		"missing",
		"read failure",
		"outbox failure",
		"invalid id",
		"missing actor",
	} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			owner := identity.NewUserID()
			req := uploadservice.DeleteRequest{FileID: 123, UserRequestID: owner}
			file := model.File{ID: 123, FileName: "source.png", FolderPath: "uploads/admin/12"}
			switch phase {
			case "invalid id":
				req.FileID = 0
			case "missing actor":
				req.UserRequestID = identity.UserIDNil
			default:
				if phase == "missing" {
					file = model.File{}
				}
				var readErr error
				if phase == "read failure" {
					readErr = failure
				}
				ts.mockFileRepository.EXPECT().GetByID(ctx, int64(123)).Return(file, readErr)
				if phase != "missing" && phase != "read failure" {
					if phase == "afterjobs" {
						req.AfterJobs = shared.NewFileEventAfterJobs("deleted_bind", owner)
					}
					expected, err := deletedfile.MarshalPayload(deletedfile.NewPayload(file.ID, owner, file.GetFullPath(), req.AfterJobs...))
					require.NoError(t, err)
					var putErr error
					if phase == "outbox failure" {
						putErr = failure
					}
					ts.mockOutboxPutter.EXPECT().Put(gomock.Any(),
						deletedfile.JobName,
						expected,
						gomock.Any()).Return(outboxtypes.NewJobID(),
						putErr)
				}
			}
			err := ts.svc.DeleteFile(ctx, req)
			switch phase {
			case "success", "afterjobs", "missing":
				require.NoError(t, err)
			case "read failure", "outbox failure":
				require.ErrorIs(t, err, failure)
			default:
				require.Error(t, err)
			}
		})
	}
}

func TestServicePrimaryRequiresExistingBinding(t *testing.T) {
	for _, phase := range []string{"success", "wrong object", "missing"} {
		t.Run(phase, func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			file := model.File{ID: 77, ObjectType: shared.ObjectTypeExercise, ObjectID: pointer.To(shared.FileObjectID(901))}
			switch phase {
			case "wrong object":
				file.ObjectID = pointer.To(shared.FileObjectID(33))
			case "missing":
				file = model.File{}
			}
			ts.mockFileRepository.EXPECT().GetByID(ctx, int64(77)).Return(file, nil)
			if phase == "success" {
				executeTx(ts)
				gomock.InOrder(ts.mockFileRepository.EXPECT().ClearPrimary(gomock.Any(),
					"exercise",
					int64(901),
					int64(77)).Return(nil),
					ts.mockFileRepository.EXPECT().SetPrimary(gomock.Any(),
						int64(77),
						"exercise",
						int64(901)).Return(nil))
			}
			err := ts.svc.SetPrimary(ctx,
				uploadservice.SetPrimaryRequest{
					FileID:     77,
					ObjectType: shared.ObjectTypeExercise,
					ObjectID:   901,
				})
			switch phase {
			case "success":
				require.NoError(t, err)
			case "wrong object":
				require.ErrorIs(t, err, model.ErrObjectBindingMismatch)
			default:
				require.ErrorIs(t, err, uploadservice.ErrFileNotFound)
			}
		})
	}
}

func TestServiceBatchErrorUnwrap(t *testing.T) {
	original := uploadservice.ClientError{Message: "denied"}
	err := &uploadservice.BatchError{FailedIndex: 2, Err: original}
	var clientErr uploadservice.ClientError
	require.ErrorAs(t, err, &clientErr)
	require.Equal(t, original.Message, clientErr.Message)
}
