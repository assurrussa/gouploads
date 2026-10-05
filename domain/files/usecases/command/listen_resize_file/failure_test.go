package listenresizefile_test

import (
	"context"
	"errors"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
	listenresizefilemocks "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file/mocks"
	eventstreammocks "github.com/assurrussa/gouploads/internal/events/mocks"
)

type failureRepo struct {
	*listenresizefilemocks.MockfileRepository
	file    model.File
	changed bool
	err     error
}

func (r failureRepo) MarkMediaFailed(context.Context, int64) (model.File, bool, error) {
	return r.file, r.changed, r.err
}

func TestFailureCallbackPersistsWithoutArtifacts(t *testing.T) {
	for _, tc := range []struct {
		name    string
		changed bool
		err     error
	}{
		{"new terminal failure", true, nil},
		{"duplicate or already completed", false, nil},
		{"database unavailable", false, errors.New("database unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctrl := gomock.NewController(t)
			file := testshelpers.CreateFile(t)
			repo := failureRepo{
				MockfileRepository: listenresizefilemocks.NewMockfileRepository(ctrl),
				file:               file, changed: tc.changed, err: tc.err,
			}
			outbox := listenresizefilemocks.NewMockoutboxPutter(ctrl)
			events := eventstreammocks.NewMockPublisher(ctrl)
			if tc.changed {
				events.EXPECT().Publish(gomock.Any(), file.GetData().Uploader.UserUUID, gomock.Any()).Return(nil)
			}
			uc := listenresizefile.Must(listenresizefile.NewOptions(repo, outbox, events, logger.Discard()))
			_, err := uc.Handle(context.Background(), listenresizefile.Request{ExternalID: file.ID, Status: "failed"})
			if tc.err != nil {
				require.ErrorIs(t, err, tc.err)
			} else {
				require.NoError(t, err)
			}
		})
	}
}

func TestFailureCallbackRequiresDurableRepository(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	_, err := ts.useCase.Handle(ctx, listenresizefile.Request{ExternalID: 1, Status: "failed"})
	require.ErrorContains(t, err, "failure-capable")
}

func TestCompletedCallbackRequiresNonemptyArtifacts(t *testing.T) {
	require.Error(t, (listenresizefile.Request{ExternalID: 1, Status: "done"}).Validate())
	require.NoError(t, (listenresizefile.Request{ExternalID: 1, Status: "failed"}).Validate())
	require.Error(t, (listenresizefile.Request{Status: "failed"}).Validate())
}

func TestCompletedFileIgnoresDelayedSuccess(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	file := testshelpers.CreateFile(t)
	file.Data.Uploader.Status = shared.FileUploadTaskStatusCompleted
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil)
	_, err := ts.useCase.Handle(ctx, listenresizefile.Request{
		ExternalID: file.ID, Status: "done",
		Artifacts: []listenresizefile.Artifact{{Preset: "main", URL: "https://example.test/main"}},
	})
	require.NoError(t, err)
}
