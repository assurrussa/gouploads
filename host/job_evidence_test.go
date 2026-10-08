package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

type jobEvidenceReader struct {
	snapshot host.FileJobSnapshot
	err      error
	calls    int
}

func (r *jobEvidenceReader) GetFileJobs(context.Context, int64, host.FileJobOperation) (host.FileJobSnapshot, error) {
	r.calls++
	return r.snapshot, r.err
}

func TestFileJobDiagnosticsKeepCoverageAndOutcomeIndependent(t *testing.T) {
	for _, tc := range []struct {
		name      string
		jobs      []host.FileJobEvidence
		truncated bool
		coverage  string
	}{
		{name: "legacy or unmapped", coverage: "historical_unmapped"},
		{name: "truncated empty cannot mean no jobs", truncated: true, coverage: "partial"},
		{
			name: "missing after ack or removal is unknown",
			jobs: []host.FileJobEvidence{{State: host.FileJobUnknown}}, coverage: "partial",
		},
		{
			name: "failure does not establish storage outcome",
			jobs: []host.FileJobEvidence{{State: host.FileJobFailed}}, coverage: "partial",
		},
		{
			name: "expired lease is not quiescence",
			jobs: []host.FileJobEvidence{{State: host.FileJobLeaseExpired}}, coverage: "partial",
		},
		{
			name: "mixed jobs remain partial",
			jobs: []host.FileJobEvidence{{State: host.FileJobAvailable}, {State: host.FileJobLeased}}, coverage: "partial",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			r := &jobEvidenceReader{snapshot: host.FileJobSnapshot{
				ObservedAt: time.Now(), Jobs: tc.jobs, Truncated: tc.truncated,
			}}
			result, err := host.InspectFileJobs(t.Context(), r, 13, host.FileJobOriginalFinalization)
			require.NoError(t, err)
			require.Equal(t, 1, r.calls)
			require.Equal(t, tc.coverage, result.Coverage)
			require.Equal(t, "unknown", result.Outcome)
			require.Equal(t, "unknown", result.Quiescence)
			require.Equal(t, r.snapshot, result.FileJobSnapshot)
		})
	}
}

func TestFileJobDiagnosticErrorsAreSanitized(t *testing.T) {
	for _, sourceErr := range []error{
		errors.New("private database path or credentials"),
		fmt.Errorf("private detail: %w", context.Canceled),
		fmt.Errorf("private detail: %w", context.DeadlineExceeded),
	} {
		r := &jobEvidenceReader{err: sourceErr}
		result, err := host.InspectFileJobs(t.Context(), r, 13, host.FileJobDeletion)
		require.Error(t, err)
		require.NotContains(t, err.Error(), "private")
		require.Equal(t, "unavailable", result.Coverage)
		require.Empty(t, result.Jobs)
	}
	var nilReader *jobEvidenceReader
	_, err := host.InspectFileJobs(t.Context(), nilReader, 13, host.FileJobDeletion)
	require.ErrorIs(t, err, host.ErrDiagnosticUnavailable)
}

func TestFileJobDiagnosticsValidateBeforeReading(t *testing.T) {
	r := &jobEvidenceReader{}
	_, err := host.InspectFileJobs(t.Context(), r, 0, host.FileJobDeletion)
	require.ErrorIs(t, err, host.ErrInvalidDiagnosticFileID)
	_, err = host.InspectFileJobs(t.Context(), r, 1, "unsupported")
	require.ErrorIs(t, err, host.ErrInvalidDiagnosticOperation)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = host.InspectFileJobs(ctx, r, 1, host.FileJobDeletion)
	require.ErrorIs(t, err, context.Canceled)
	require.Zero(t, r.calls)
}

func TestFileJobJSONContainsOnlyNormalizedEvidence(t *testing.T) {
	result, err := host.InspectFileJobs(t.Context(), &jobEvidenceReader{}, 13, host.FileJobDeletion)
	require.NoError(t, err)
	body, err := json.Marshal(result)
	require.NoError(t, err)
	for _, absent := range []string{"payload", "leaseToken", "reason", "exception", "url", "path", "safeTo"} {
		require.NotContains(t, string(body), absent)
	}
}
