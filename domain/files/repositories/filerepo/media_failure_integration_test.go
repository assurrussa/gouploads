//go:build integration

package filerepo_test

import (
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/shared"
)

func TestIntegration_MediaFailurePersistsAndDoesNotDowngradeCompletion(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	file := createModel("media-failure", "media-failure", time.Now().UTC())
	data := file.GetData()
	data.Uploader.Status = shared.FileUploadTaskStatusQueued
	file.SetData(data)
	id, err := ts.repo.Create(ctx, file)
	require.NoError(t, err)
	failed, changed, err := ts.repo.MarkMediaFailed(ctx, id)
	require.NoError(t, err)
	require.True(t, changed)
	require.Equal(t, shared.FileUploadTaskStatusFailed, failed.GetData().Uploader.Status)
	saved, err := ts.repo.GetByID(ctx, id)
	require.NoError(t, err)
	require.Equal(t, shared.FileUploadTaskStatusFailed, saved.GetData().Uploader.Status)
	_, changed, err = ts.repo.MarkMediaFailed(ctx, id)
	require.NoError(t, err)
	require.False(t, changed)
	saved.Data.Uploader = shared.FileUploader{}
	saved.Data.Presets = map[shared.PresetName]shared.FilePreset{shared.FilePresetMainName: {}}
	require.NoError(t, ts.repo.Update(ctx, id, saved))
	_, changed, err = ts.repo.MarkMediaFailed(ctx, id)
	require.NoError(t, err)
	require.False(t, changed)
	saved, err = ts.repo.GetByID(ctx, id)
	require.NoError(t, err)
	require.True(t, saved.IsUploadCompleted())
	require.Empty(t, saved.GetData().Uploader.Status)
}
