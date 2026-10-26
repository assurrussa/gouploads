package uploadservice_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/config"
	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	mocks "github.com/assurrussa/gouploads/domain/files/service/uploadservice/mocks"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	filestorage "github.com/assurrussa/gouploads/infrastructure/storage/files"
	"github.com/assurrussa/gouploads/internal/identity"
)

type serviceFinalizationRepo struct {
	*mocks.MockfileRepository
	binding string
	file    model.File
}

func (r *serviceFinalizationRepo) FinalizeUpload(
	ctx context.Context, _, binding string, _ bool,
	create func(context.Context) (model.File, error),
) (model.File, error) {
	if r.binding != "" {
		if r.binding != binding {
			return model.File{}, errors.New("binding mismatch")
		}
		return r.file, nil
	}
	file, err := create(ctx)
	if err == nil {
		r.file = file
		r.binding = binding
	}
	return file, err
}

func TestServiceReaderUsesExistingPipelineAndIsolatesReplay(t *testing.T) {
	for _, mode := range []config.ProcessingMode{config.ProcessingOriginalOnly, config.ProcessingMediaResizer} {
		t.Run(string(mode), func(t *testing.T) {
			ctx, cancel, ts := NewTestRepoSuite(t)
			defer cancel()
			header := testshelpers.MakeFileHeaderImage(t, "photo", "original.png", "image/png")
			reader, err := header.Open()
			require.NoError(t, err)
			body, err := io.ReadAll(reader)
			require.NoError(t, err)
			require.NoError(t, reader.Close())
			repo := &serviceFinalizationRepo{MockfileRepository: ts.mockFileRepository}
			queue := &associatedUploadQueue{MockoutboxPutter: ts.mockOutboxPutter}
			svc, err := uploadservice.NewWithProcessing(uploadservice.NewOptions(ts.mockTransactor,
				queue, repo, logger.Discard(), ts.mockFileStorage), mode)
			require.NoError(t, err)
			ts.mockFileStorage.EXPECT().SaveTemp(gomock.Any(), gomock.Any()).Return(filestorage.StoredFile{
				RelativePath: "tmp/uploads/seo_cms_asset/12/source.png",
				Size:         int64(len(body)),
				MimeType:     "image/png",
			}, nil).Times(1)
			executeTx(ts)
			ts.mockFileRepository.EXPECT().Create(gomock.Any(), gomock.Any()).Return(int64(21), nil).Times(1)
			principal := shared.ServicePrincipal{SiteID: uuid.New(), ClientID: "cms-service"}
			req := uploadservice.ReaderRequest{
				Service:         &principal,
				ObjectType:      "seo_cms_asset",
				ObjectID:        12,
				FinalizationKey: uuid.NewString(),
				Config:          uploadservice.DefaultFileUploadConfig(),
				AfterJobs:       []shared.FileEventAfterJob{{JobName: "asset_complete", Payload: `{"generation":"fixture"}`}},
			}
			input := uploadservice.ReaderUploadInput{OriginalName: "original.png", Size: int64(len(body)), Reader: bytes.NewReader(body)}
			file, err := svc.UploadReader(ctx, req, input)
			require.NoError(t, err)
			require.Nil(t, file.ManagerID)
			require.Nil(t, file.UserID)
			require.Equal(t, &principal, file.GetData().Service)
			require.Equal(t, shared.UserTypeService, file.GetData().Uploader.Type)
			require.NoError(t, file.GetData().Uploader.Validate())
			require.Equal(t, req.AfterJobs, file.GetData().Uploader.AfterJobs)
			require.Equal(t, int64(21), queue.file.ID)
			replay, err := svc.UploadReader(ctx, req, input)
			require.NoError(t, err)
			require.Equal(t, file.ID, replay.ID)
			changed := principal
			changed.SiteID = uuid.New()
			req.Service = &changed
			_, err = svc.UploadReader(ctx, req, input)
			require.ErrorContains(t, err, "binding mismatch")
			changed = principal
			changed.ClientID = "another-client"
			req.Service = &changed
			_, err = svc.UploadReader(ctx, req, input)
			require.ErrorContains(t, err, "binding mismatch")
		})
	}
}

func TestServiceReaderValidation(t *testing.T) {
	valid := func() uploadservice.ReaderRequest {
		return uploadservice.ReaderRequest{
			Service:    &shared.ServicePrincipal{SiteID: uuid.New(), ClientID: "cms"},
			ObjectType: "seo_cms_asset",
			ObjectID:   12,
		}
	}
	require.NoError(t, valid().Validate())
	for name, change := range map[string]func(*uploadservice.ReaderRequest){
		"site":        func(r *uploadservice.ReaderRequest) { r.Service.SiteID = uuid.Nil },
		"client":      func(r *uploadservice.ReaderRequest) { r.Service.ClientID = " " },
		"manager":     func(r *uploadservice.ReaderRequest) { r.ManagerID = 1 },
		"user":        func(r *uploadservice.ReaderRequest) { r.UserID = 1 },
		"uuid":        func(r *uploadservice.ReaderRequest) { r.UploaderUUID = identity.NewUserID() },
		"replacement": func(r *uploadservice.ReaderRequest) { r.DeletedID = 1 },
		"object":      func(r *uploadservice.ReaderRequest) { r.ObjectID = 0 },
		"type":        func(r *uploadservice.ReaderRequest) { r.ObjectType = "../site" },
		"default-job": func(r *uploadservice.ReaderRequest) { r.AfterJobs = []shared.FileEventAfterJob{{JobName: "callback"}} },
		"recipient": func(r *uploadservice.ReaderRequest) {
			r.AfterJobs = []shared.FileEventAfterJob{{JobName: "callback", Payload: "{}", UserID: identity.NewUserID()}}
		},
	} {
		t.Run(name, func(t *testing.T) { r := valid(); change(&r); require.Error(t, r.Validate()) })
	}
	require.Error(t, (uploadservice.ReaderRequest{ObjectType: "seo_cms_asset", ObjectID: 12}).Validate())
	require.NoError(t, (uploadservice.ReaderRequest{
		UploaderUUID: identity.NewUserID(), ManagerID: 1,
		ObjectType: "admin",
	}).Validate())
}
