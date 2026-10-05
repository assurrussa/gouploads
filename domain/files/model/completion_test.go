package model_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
)

func TestUploadCompletionRecognizesClearedUploader(t *testing.T) {
	file := model.File{}
	require.False(t, file.IsUploadCompleted())
	file.Data = &model.FileData{Presets: map[shared.PresetName]shared.FilePreset{shared.FilePresetMainName: {}}}
	require.True(t, file.IsUploadCompleted())
	file.Data.Uploader.Status = shared.FileUploadTaskStatusProcessing
	require.False(t, file.IsUploadCompleted())
	file.Data.Uploader.Status = shared.FileUploadTaskStatusCompleted
	require.True(t, file.IsUploadCompleted())
}
