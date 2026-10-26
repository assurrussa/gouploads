//go:build integration

package filerepo_test

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
)

func TestIntegrationServiceProvenanceSurvivesFinalizationReplay(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	principal := shared.ServicePrincipal{SiteID: uuid.New(), ClientID: "cms"}
	key, binding := uuid.NewString(), strings.Repeat("a", 64)
	calls := 0
	create := func(ctx context.Context) (model.File, error) {
		calls++
		file := hardeningFile(9001)
		file.Data = &model.FileData{
			Service: &principal,
			Uploader: shared.FileUploader{
				Type: shared.UserTypeService, Service: &principal, Status: shared.FileUploadTaskStatusQueued,
			},
		}
		id, err := ts.repo.Create(ctx, file)
		file.ID = id
		return file, err
	}
	file, err := ts.repo.FinalizeUpload(ctx, key, binding, false, create)
	require.NoError(t, err)
	saved, err := ts.repo.GetByID(ctx, file.ID)
	require.NoError(t, err)
	require.Nil(t, saved.UserID)
	require.Nil(t, saved.ManagerID)
	require.Equal(t, &principal, saved.GetData().Service)
	require.NoError(t, saved.GetData().Uploader.Validate())
	saved.Data.Uploader = shared.FileUploader{}
	require.NoError(t, ts.repo.Update(ctx, file.ID, saved))
	replay, err := ts.repo.FinalizeUpload(ctx, key, binding, false, create)
	require.NoError(t, err)
	require.Equal(t, file.ID, replay.ID)
	require.Equal(t, &principal, replay.GetData().Service)
	require.Equal(t, 1, calls)
	_, err = ts.repo.FinalizeUpload(ctx, key, strings.Repeat("b", 64), false, create)
	require.ErrorIs(t, err, model.ErrFinalizationConflict)
}
