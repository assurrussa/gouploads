package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

type diagnosticReader struct {
	snapshot host.FileLifecycleSnapshot
	err      error
	calls    int
	id       int64
}

func (r *diagnosticReader) GetFileLifecycle(_ context.Context, id int64) (host.FileLifecycleSnapshot, error) {
	r.calls++
	r.id = id
	return r.snapshot, r.err
}

func TestDiagnoseFileStates(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name     string
		snapshot host.FileLifecycleSnapshot
		file     host.DiagnosticState
		upload   host.DiagnosticState
		deletion host.DiagnosticState
	}{
		{
			"queued",
			host.FileLifecycleSnapshot{FileExists: true, UploadStatus: host.FileUploadTaskStatusQueued},
			host.DiagnosticPresent, host.DiagnosticQueued, host.DiagnosticNone,
		},
		{
			"processing",
			host.FileLifecycleSnapshot{FileExists: true, UploadStatus: host.FileUploadTaskStatusProcessing},
			host.DiagnosticPresent, host.DiagnosticProcessing, host.DiagnosticNone,
		},
		{
			"completed",
			host.FileLifecycleSnapshot{FileExists: true, UploadStatus: host.FileUploadTaskStatusCompleted},
			host.DiagnosticPresent, host.DiagnosticCompleted, host.DiagnosticNone,
		},
		{
			"legacy completed",
			host.FileLifecycleSnapshot{FileExists: true, HasPresets: true},
			host.DiagnosticPresent, host.DiagnosticCompleted, host.DiagnosticNone,
		},
		{
			"failed",
			host.FileLifecycleSnapshot{FileExists: true, UploadStatus: host.FileUploadTaskStatusFailed, HasPresets: true},
			host.DiagnosticPresent, host.DiagnosticFailed, host.DiagnosticNone,
		},
		{
			"deleting",
			host.FileLifecycleSnapshot{FileExists: true, FileDeleted: true, DeletionPlanned: true},
			host.DiagnosticDeleted, host.DiagnosticUnknown, host.DiagnosticDeleting,
		},
		{
			"deleted retained plan",
			host.FileLifecycleSnapshot{DeletionPlanned: true, DeletionCompleted: true},
			host.DiagnosticMissing, host.DiagnosticUnknown, host.DiagnosticCompleted,
		},
		{"missing", host.FileLifecycleSnapshot{}, host.DiagnosticMissing, host.DiagnosticUnknown, host.DiagnosticNone},
		{
			"missing metadata",
			host.FileLifecycleSnapshot{FileExists: true},
			host.DiagnosticPresent, host.DiagnosticUnknown, host.DiagnosticNone,
		},
		{
			"future status",
			host.FileLifecycleSnapshot{FileExists: true, UploadStatus: "secret/path", HasPresets: true},
			host.DiagnosticPresent, host.DiagnosticUnknown, host.DiagnosticNone,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := &diagnosticReader{snapshot: tc.snapshot}
			result, err := host.DiagnoseFile(t.Context(), reader, 42)
			require.NoError(t, err)
			require.Equal(t, host.FileDiagnosis{
				FileID: 42, File: tc.file, Upload: tc.upload,
				Finalization: host.DiagnosticNone, Deletion: tc.deletion, Job: host.DiagnosticUnavailable,
			}, result)
			require.Equal(t, 1, reader.calls)
			require.EqualValues(t, 42, reader.id)
			encoded, err := json.Marshal(result)
			require.NoError(t, err)
			require.NotContains(t, string(encoded), "secret/path")
		})
	}
}

func TestDiagnoseFileRecordedHandoffIsNotCompletion(t *testing.T) {
	t.Parallel()
	reader := &diagnosticReader{snapshot: host.FileLifecycleSnapshot{
		FileExists: true, UploadStatus: host.FileUploadTaskStatusQueued, FinalizationRecorded: true,
	}}
	result, err := host.DiagnoseFile(t.Context(), reader, 42)
	require.NoError(t, err)
	require.Equal(t, host.DiagnosticRecorded, result.Finalization)
	require.Equal(t, host.DiagnosticQueued, result.Upload)
	reader.snapshot = host.FileLifecycleSnapshot{FinalizationRecorded: true}
	result, err = host.DiagnoseFile(t.Context(), reader, 42)
	require.NoError(t, err)
	require.Equal(t, host.DiagnosticRecorded, result.Finalization)
	require.Equal(t, host.DiagnosticMissing, result.File)
}

func TestDiagnoseFileUnavailableAndValidation(t *testing.T) {
	t.Parallel()
	reader := &diagnosticReader{snapshot: host.FileLifecycleSnapshot{FileExists: true}, err: errors.New("secret database path")}
	result, err := host.DiagnoseFile(t.Context(), reader, 42)
	require.ErrorIs(t, err, host.ErrDiagnosticUnavailable)
	require.NotContains(t, err.Error(), "secret")
	require.Equal(t, host.FileDiagnosis{
		FileID: 42, File: host.DiagnosticUnavailable, Upload: host.DiagnosticUnavailable,
		Finalization: host.DiagnosticUnavailable, Deletion: host.DiagnosticUnavailable, Job: host.DiagnosticUnavailable,
	}, result)
	for _, id := range []int64{0, -1} {
		_, err = host.DiagnoseFile(t.Context(), reader, id)
		require.ErrorIs(t, err, host.ErrInvalidDiagnosticFileID)
	}
	require.Equal(t, 1, reader.calls)
	var nilReader *diagnosticReader
	_, err = host.DiagnoseFile(t.Context(), nilReader, 42)
	require.ErrorIs(t, err, host.ErrDiagnosticUnavailable)
	_, err = host.DiagnoseFile(t.Context(), nil, 42)
	require.ErrorIs(t, err, host.ErrDiagnosticUnavailable)
}

func TestDiagnoseFileCancellation(t *testing.T) {
	t.Parallel()
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	reader := &diagnosticReader{}
	_, err := host.DiagnoseFile(ctx, reader, 42)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, reader.calls)
	for _, sourceErr := range []error{context.Canceled, context.DeadlineExceeded} {
		reader.err = fmt.Errorf("secret path: %w", sourceErr)
		_, err = host.DiagnoseFile(t.Context(), reader, 42)
		require.ErrorIs(t, err, sourceErr)
		require.NotContains(t, err.Error(), "secret")
	}
}

func ExampleDiagnoseFile() {
	// A host authorizes file 42 before invoking this read-only operation.
	reader := &diagnosticReader{snapshot: host.FileLifecycleSnapshot{
		FileExists: true, UploadStatus: host.FileUploadTaskStatusQueued, FinalizationRecorded: true,
	}}
	result, err := host.DiagnoseFile(context.Background(), reader, 42)
	if err != nil {
		return
	}
	fmt.Println(result.File, result.Upload, result.Finalization, result.Deletion, result.Job)
	// Output: present queued recorded none unavailable
}
