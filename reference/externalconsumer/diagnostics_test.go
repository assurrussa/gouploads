package externalconsumer_test

import (
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

var _ host.FileDiagnosticReader = (*host.FileRepo)(nil)

func TestDiagnosticsPublicSurface(t *testing.T) {
	t.Parallel()
	result, err := host.DiagnoseFile(t.Context(), nil, 7)
	require.ErrorIs(t, err, host.ErrDiagnosticUnavailable)
	require.Equal(t, host.DiagnosticUnavailable, result.Job)
	require.EqualValues(t, 7, result.FileID)
	var snapshot host.FileLifecycleSnapshot
	require.False(t, snapshot.FinalizationRecorded)
}
