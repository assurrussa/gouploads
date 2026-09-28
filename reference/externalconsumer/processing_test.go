package externalconsumer_test

import (
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
