package uploadservice_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	finalizeoriginal "github.com/assurrussa/gouploads/domain/files/outbox/finalize_original"
	sendresize "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	mocks "github.com/assurrussa/gouploads/domain/files/service/uploadservice/mocks"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/identity"
)

// A handwritten adapter extends the generated storage port without modifying it.
type readableStorage struct {
	*mocks.MockfileStorage
	body  []byte
	opens int
}

func (s *readableStorage) Open(context.Context, string) (io.ReadCloser, error) {
	s.opens++
	return io.NopCloser(bytes.NewReader(s.body)), nil
}

func TestConfiguredUploaderDefaultsToOriginalForEveryIngress(t *testing.T) {
	for _, ingress := range []string{"single", "batch", "reader", "stored"} {
		t.Run(ingress, func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			header := testshelpers.MakeFileHeaderImage(t, "photo", "original.png", "image/png")
			reader, err := header.Open()
			require.NoError(t, err)
			body, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			storage := &readableStorage{MockfileStorage: ts.mockFileStorage, body: body}
			svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(ts.mockTransactor,
				ts.mockOutboxPutter,
				ts.mockFileRepository,
				logger.Discard(),
				storage),
				"")
			require.NoError(t, err)
			const key = "tmp/uploads/admin/12/source.png"
			if ingress != "stored" {
				ts.mockFileStorage.EXPECT().SaveTemp(gomock.Any(),
					gomock.Any()).Return(filestorage.StoredFile{
					RelativePath: key,
					Size:         int64(len(body)),
					MimeType:     "image/png",
				},
					nil)
			}
			executeTx(ts)
			ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(21), nil)
			payload, err := finalizeoriginal.MarshalPayload(finalizeoriginal.Payload{FileID: 21})
			require.NoError(t, err)
			ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), finalizeoriginal.JobName, payload, gomock.Any())
			req := uploadservice.ReaderRequest{
				UploaderUUID: identity.NewUserID(),
				ManagerID:    12,
				ObjectType:   shared.ObjectTypeAdmin,
				ObjectID:     12,
				Config:       uploadservice.DefaultFileUploadConfig(),
			}
			req.Config.SkipResizer = true
			var result model.File
			switch ingress {
			case "single":
				result,
					err = svc.UploadSingle(ctx,
					uploadservice.SingleRequest{
						UploaderUUID: req.UploaderUUID,
						ManagerID:    req.ManagerID,
						ObjectType:   req.ObjectType,
						ObjectID:     req.ObjectID,
						Config:       req.Config,
						FileHeader:   header,
					})
			case "batch":
				var files []model.File
				files,
					err = svc.UploadBatch(ctx,
					uploadservice.BatchRequest{
						UploaderUUID: req.UploaderUUID,
						ManagerID:    req.ManagerID,
						ObjectType:   req.ObjectType,
						ObjectID:     req.ObjectID,
						Config:       req.Config,
						FileHeaders:  []*multipart.FileHeader{header},
					})
				require.NoError(t, err)
				require.Len(t, files, 1)
				result = files[0]
			case "reader":
				result,
					err = svc.UploadReader(ctx,
					req,
					uploadservice.ReaderUploadInput{
						OriginalName: "original.png",
						Size:         int64(len(body)),
						Reader:       bytes.NewReader(body),
					})
			case "stored":
				result,
					err = svc.UploadStored(ctx,
					req,
					uploadservice.UploadedFile{
						OriginalName: "original.png",
						FileName:     "source.png",
						Path:         key,
						FolderPath:   "tmp/uploads/admin/12",
						Size:         int64(len(body)),
						MimeType:     "image/png",
						FileType:     model.FileTypeImage,
					})
				require.Equal(t, 1, storage.opens)
			}
			require.NoError(t, err)
			require.Equal(t, int64(21), result.ID)
			require.Equal(t, shared.FileUploadTaskStatusQueued, result.GetData().Uploader.Status)
		})
	}
}

func TestConfiguredUploaderExplicitMediaPreservesSkipFlag(t *testing.T) {
	ctx, cancel, ts := NewTestRepoSuite(t)
	defer cancel()
	body := []byte("%PDF-1.7\ncomplete test PDF bytes\n")
	storage := &readableStorage{MockfileStorage: ts.mockFileStorage, body: body}
	svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(ts.mockTransactor,
		ts.mockOutboxPutter,
		ts.mockFileRepository,
		logger.Discard(),
		storage),
		config.ProcessingMediaResizer)
	require.NoError(t, err)
	executeTx(ts)
	ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(21), nil)
	const key = "tmp/uploads/admin/12/source.pdf"
	payload, err := sendresize.MarshalPayload(sendresize.NewPayload(21, key, true))
	require.NoError(t, err)
	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), sendresize.JobName, payload, gomock.Any())
	_,
		err = svc.UploadStored(ctx,
		uploadservice.ReaderRequest{
			UploaderUUID: identity.NewUserID(),
			ManagerID:    12,
			ObjectType:   shared.ObjectTypeAdmin,
			ObjectID:     12,
			Config:       &uploadservice.FileUploadConfig{SkipResizer: true},
		},
		uploadservice.UploadedFile{
			OriginalName: "source.pdf",
			FileName:     "source.pdf",
			FolderPath:   "tmp/uploads/admin/12",
			Path:         key,
			MimeType:     "application/pdf",
			FileType:     model.FileTypePdf,
			Size:         int64(len(body)),
		})
	require.NoError(t, err)
	require.Equal(t, 1, storage.opens)
}

func TestStoredUploadRunsValidatorsAndRejectsForgedMIME(t *testing.T) {
	for _, forged := range []bool{false, true} {
		t.Run(map[bool]string{true: "forged", false: "validator"}[forged], func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			body := []byte("%PDF-1.7\nprivate PDF\n")
			if forged {
				body = []byte("plain text pretending to be PDF")
			}
			storage := &readableStorage{MockfileStorage: ts.mockFileStorage, body: body}
			calls := 0
			opts := uploadservice.NewOptions(ts.mockTransactor,
				ts.mockOutboxPutter,
				ts.mockFileRepository,
				logger.Discard(),
				storage,
				uploadservice.WithValidators([]uploadservice.UploadValidator{uploadservice.UploadValidatorFunc(func(context.Context,
					uploadservice.UploadValidationInput,
				) error {
					calls++
					return errors.New("policy denied")
				})}))
			svc, err := uploadservice.NewWithProcessing(opts, "")
			require.NoError(t, err)
			_,
				err = svc.UploadStored(ctx,
				uploadservice.ReaderRequest{
					UploaderUUID: identity.NewUserID(),
					ManagerID:    12,
					ObjectType:   shared.ObjectTypeAdmin,
					ObjectID:     12,
				},
				uploadservice.UploadedFile{
					OriginalName: "source.pdf",
					FileName:     "source.pdf",
					Path:         "tmp/uploads/source.pdf",
					Size:         int64(len(body)),
					MimeType:     "application/pdf",
				})
			require.Error(t, err)
			if forged {
				require.Zero(t, calls)
			} else {
				require.Equal(t, 1, calls)
			}
		})
	}
}
