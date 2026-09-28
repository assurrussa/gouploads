package uploadservice_test

import (
	"bytes"
	"context"
	"io"
	"mime/multipart"
	"testing"

	logger "github.com/assurrussa/gologger"
	sharedtypes "github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	finalizeoriginal "github.com/assurrussa/gouploads/domain/files/outbox/finalize_original"
	sendresize "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
)

func TestConfiguredUploaderDefaultsToOriginalForEveryIngress(t *testing.T) {
	for _, ingress := range []string{"single", "batch", "reader", "stored"} {
		t.Run(ingress, func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(
				ts.mockTransactor, ts.mockOutboxPutter, ts.mockFileRepository, logger.Discard(), ts.mockFileStorage,
			), "")
			require.NoError(t, err)
			header := testshelpers.MakeFileHeaderImage(t, "photo", "original.png", "image/png")
			reader, err := header.Open()
			require.NoError(t, err)
			body, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			const key = "tmp/uploads/admin/12/source.png"
			if ingress != "stored" {
				ts.mockFileStorage.EXPECT().SaveTemp(gomock.Any(), gomock.Any()).Return(filestorage.StoredFile{
					RelativePath: key, Size: int64(len(body)), MimeType: "image/png",
				}, nil)
			}
			ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
				func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) },
			)
			ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(21), nil)
			payload, err := finalizeoriginal.MarshalPayload(finalizeoriginal.Payload{FileID: 21})
			require.NoError(t, err)
			ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), finalizeoriginal.JobName, payload, gomock.Any())
			req := uploadservice.ReaderRequest{
				UploaderUUID: sharedtypes.NewUserID(), ManagerID: 12, ObjectType: shared.ObjectTypeAdmin, ObjectID: 12,
				Config: uploadservice.DefaultFileUploadConfig(),
			}
			// This old flag must not select a processing mode.
			req.Config.SkipResizer = true
			var result model.File
			switch ingress {
			case "single":
				result, err = svc.UploadSingle(ctx, uploadservice.SingleRequest{
					UploaderUUID: req.UploaderUUID, ManagerID: req.ManagerID, ObjectType: req.ObjectType,
					ObjectID: req.ObjectID, Config: req.Config, FileHeader: header,
				})
			case "batch":
				var files []model.File
				files, err = svc.UploadBatch(ctx, uploadservice.BatchRequest{
					UploaderUUID: req.UploaderUUID, ManagerID: req.ManagerID, ObjectType: req.ObjectType,
					ObjectID: req.ObjectID, Config: req.Config, FileHeaders: []*multipart.FileHeader{header},
				})
				require.NoError(t, err)
				require.Len(t, files, 1)
				result = files[0]
			case "reader":
				result, err = svc.UploadReader(ctx, req, uploadservice.ReaderUploadInput{
					OriginalName: "original.png", Size: int64(len(body)), Reader: bytes.NewReader(body),
				})
			case "stored":
				result, err = svc.UploadStored(ctx, req, uploadservice.UploadedFile{
					OriginalName: "original.png", FileName: "source.png", Path: key,
					FolderPath: "tmp/uploads/admin/12", Size: int64(len(body)), MimeType: "image/png", FileType: model.FileTypeImage,
				})
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
	svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(
		ts.mockTransactor, ts.mockOutboxPutter, ts.mockFileRepository, logger.Discard(), ts.mockFileStorage,
	), config.ProcessingMediaResizer)
	require.NoError(t, err)
	ts.mockTransactor.EXPECT().RunInTx(gomock.Any(), gomock.Any()).DoAndReturn(
		func(ctx context.Context, fn func(context.Context) error) error { return fn(ctx) },
	)
	ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(21), nil)
	const key = "tmp/uploads/admin/12/source.pdf"
	payload, err := sendresize.MarshalPayload(sendresize.NewPayload(21, key, true))
	require.NoError(t, err)
	ts.mockOutboxPutter.EXPECT().Put(gomock.Any(), sendresize.JobName, payload, gomock.Any())
	_, err = svc.UploadStored(ctx, uploadservice.ReaderRequest{
		UploaderUUID: sharedtypes.NewUserID(), ManagerID: 12, ObjectType: shared.ObjectTypeAdmin, ObjectID: 12,
		Config: &uploadservice.FileUploadConfig{SkipResizer: true},
	}, uploadservice.UploadedFile{
		OriginalName: "source.pdf", FileName: "source.pdf", FolderPath: "tmp/uploads/admin/12", Path: key,
		MimeType: "application/pdf", FileType: model.FileTypePdf, Size: 32,
	})
	require.NoError(t, err)
}
