package externalconsumer_test

import (
	"context"
	"encoding/json"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestOriginalRuntimeSurface(t *testing.T) {
	var mode host.ProcessingMode
	resolved, err := mode.Resolve()
	require.NoError(t, err)
	require.Equal(t, host.ProcessingOriginalOnly, resolved)
	// No database connection is attempted when infrastructure is missing.
	runtime, err := host.NewOriginalRuntime(host.StorageConfig{}, host.OriginalRuntimeDeps{})
	require.Error(t, err)
	require.Nil(t, runtime)
}

func TestOriginalOutboxRegistrationNeedsNoMediaCommands(t *testing.T) {
	// These are registration-only sentinels; never execute uninitialized commands.
	jobs, err := host.BuildOutboxJobs(host.OutboxJobDeps{
		Logger:                  logger.Discard(),
		UseCaseFinalizeOriginal: &host.FinalizeOriginalCommand{},
		UseCaseDeleteFile:       &host.DeleteFileCommand{},
	})
	require.NoError(t, err)
	require.Len(t, jobs, 2)
	require.Equal(t, "finalize_original_file", jobs[0].Name())
}

func TestPartialMediaRegistrationFailsClosed(t *testing.T) {
	_, err := host.BuildOutboxJobs(host.OutboxJobDeps{
		Logger: logger.Discard(), UseCaseDeleteFile: &host.DeleteFileCommand{},
		UseCaseSendResize: &host.SendResizeCommand{},
	})
	require.Error(t, err)
}

func TestOriginalModeRejectsPresetsThroughPublicConfig(t *testing.T) {
	_, err := host.NewOriginalRuntime(host.StorageConfig{
		Image: host.ImagePipelineConfig{Presets: []host.ImagePresetConfig{{Name: "thumb"}}},
	}, host.OriginalRuntimeDeps{})
	require.ErrorIs(t, err, host.ErrProcessingDisabled)
}

// A host adapter only implements publication, with no subscription or Close.
type publisher struct{}

func (publisher) Publish(context.Context, host.UserID, host.Event) error { return nil }

var _ host.EventPublisher = publisher{}

func TestIdentityAndEventSurface(t *testing.T) {
	id := host.NewUserID()
	data, err := json.Marshal(id)
	require.NoError(t, err)
	var decoded host.UserID
	require.NoError(t, json.Unmarshal(data, &decoded))
	require.Equal(t, id, decoded)
	require.NoError(t, host.NewEventID().Validate())
	deps := host.OriginalRuntimeDeps{Events: publisher{}}
	require.NotNil(t, deps.Events)
}

func TestMediaCompletionAndFailureSurface(t *testing.T) {
	file := host.File{Data: &host.FileData{Presets: map[host.PresetName]host.FilePreset{"main": {}}}}
	require.True(t, file.IsUploadCompleted())
	require.NoError(t, (host.ListenResizeRequest{ExternalID: 1, Status: "failed"}).Validate())
	require.Error(t, (host.ListenResizeRequest{ExternalID: 1, Status: "done"}).Validate())
	var repo *host.FileRepo
	_ = repo.MarkMediaFailed
}

// Optional admission validation remains available through the supported facade.
var _ interface {
	ValidateUploadConfig(cfg *host.FileUploadConfig) error
} = (*host.UploadService)(nil)

func TestShortMIMEThroughHostFile(t *testing.T) {
	file := host.File{MimeType: "x"}
	require.False(t, file.IsImage())
	require.False(t, file.IsVideo())
}
