package uploadservice_test

import (
	"context"
	"io"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	mocks "github.com/assurrussa/gouploads/domain/files/service/uploadservice/mocks"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	"github.com/assurrussa/gouploads/internal/identity"
)

type associatedUploadQueue struct {
	*mocks.MockoutboxPutter
	file      model.File
	operation model.FileJobOperation
	name      string
}

func (q *associatedUploadQueue) PutFileJob(_ context.Context, file model.File, operation model.FileJobOperation,
	name, _ string, _ time.Time,
) (types.JobID, error) {
	q.file, q.operation, q.name = file, operation, name
	return types.NewJobID(), nil
}

func TestInitialProducersForwardPersistedGenerationAndOperation(t *testing.T) {
	for _, tc := range []struct {
		mode      config.ProcessingMode
		operation model.FileJobOperation
		name      string
	}{
		{config.ProcessingOriginalOnly, model.FileJobOriginalFinalization, "finalize_original_file"},
		{config.ProcessingMediaResizer, model.FileJobMediaAdmission, "send_resize_file"},
	} {
		t.Run(string(tc.mode), func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			header := testshelpers.MakeFileHeaderImage(t, "photo", "original.png", "image/png")
			reader, err := header.Open()
			require.NoError(t, err)
			body, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			storage := &readableStorage{MockfileStorage: ts.mockFileStorage, body: body}
			queue := &associatedUploadQueue{MockoutboxPutter: ts.mockOutboxPutter}
			svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(ts.mockTransactor,
				queue, ts.mockFileRepository, logger.Discard(), storage), tc.mode)
			require.NoError(t, err)
			executeTx(ts)
			ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(21), nil)
			result, err := svc.UploadStored(ctx, uploadservice.ReaderRequest{
				UploaderUUID: identity.NewUserID(), ManagerID: 12,
				ObjectType: shared.ObjectTypeAdmin, ObjectID: 12,
				Config: uploadservice.DefaultFileUploadConfig(),
			}, uploadservice.UploadedFile{
				OriginalName: "original.png", FileName: "source.png",
				Path: "tmp/uploads/admin/12/source.png", FolderPath: "tmp/uploads/admin/12",
				Size: int64(len(body)), MimeType: "image/png", FileType: model.FileTypeImage,
			})
			require.NoError(t, err)
			require.Equal(t, result, queue.file)
			require.NotEmpty(t, queue.file.Slug)
			require.Equal(t, tc.operation, queue.operation)
			require.Equal(t, tc.name, queue.name)
			// There is no legacy Put expectation: the associated method must be used.
		})
	}
}
