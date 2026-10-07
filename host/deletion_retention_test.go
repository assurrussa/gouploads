package host_test

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

type retentionReader struct {
	snapshot host.DeletionRetentionSnapshot
	err      error
	calls    int
	cutoff   *time.Time
}

func (r *retentionReader) GetDeletionRetention(_ context.Context, cutoff *time.Time) (host.DeletionRetentionSnapshot, error) {
	r.calls++
	r.cutoff = cutoff
	return r.snapshot, r.err
}

func TestInspectDeletionRetentionAvailableEmptyAndCutoff(t *testing.T) {
	t.Parallel()
	for _, withCutoff := range []bool{false, true} {
		t.Run(strconv.FormatBool(withCutoff), func(t *testing.T) {
			t.Parallel()
			reader := &retentionReader{}
			var cutoff *time.Time
			value := time.Date(2026, 1, 2, 3, 4, 5, 0, time.FixedZone("fixture", 3600))
			if withCutoff {
				cutoff = &value
			}
			result, err := host.InspectDeletionRetention(t.Context(), reader, cutoff)
			require.NoError(t, err)
			require.Equal(t, host.DiagnosticPresent, result.Source)
			require.NotNil(t, result.Snapshot)
			require.Zero(t, result.Snapshot.TotalCount)
			require.Equal(t, 1, reader.calls)
			if withCutoff {
				require.Equal(t, value.UTC(), *reader.cutoff)
				require.NotSame(t, cutoff, reader.cutoff)
				*reader.cutoff = time.Time{}
				require.False(t, value.IsZero(), "caller cutoff must not be mutated")
			} else {
				require.Nil(t, reader.cutoff)
			}
		})
	}
}

func TestInspectDeletionRetentionUnavailable(t *testing.T) {
	t.Parallel()
	cases := []struct {
		name                string
		sourceErr, expected error
	}{
		{"source details", errors.New("password and private/path"), host.ErrDiagnosticUnavailable},
		{"canceled", fmt.Errorf("private/path: %w", context.Canceled), context.Canceled},
		{"deadline", fmt.Errorf("password: %w", context.DeadlineExceeded), context.DeadlineExceeded},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			reader := &retentionReader{snapshot: host.DeletionRetentionSnapshot{TotalCount: 42}, err: tc.sourceErr}
			result, err := host.InspectDeletionRetention(t.Context(), reader, nil)
			require.ErrorIs(t, err, tc.expected)
			require.Equal(t, tc.expected.Error(), err.Error())
			require.Equal(t, host.DeletionRetentionInventory{Source: host.DiagnosticUnavailable}, result)
			require.Equal(t, 1, reader.calls)
			body, marshalErr := json.Marshal(result)
			require.NoError(t, marshalErr)
			require.JSONEq(t, `{"source":"unavailable"}`, string(body))
		})
	}
}

func TestInspectDeletionRetentionRejectsBeforeRead(t *testing.T) {
	t.Parallel()
	var typedNil *retentionReader
	for _, reader := range []host.DeletionRetentionReader{nil, typedNil} {
		result, err := host.InspectDeletionRetention(t.Context(), reader, nil)
		require.ErrorIs(t, err, host.ErrDiagnosticUnavailable)
		require.Nil(t, result.Snapshot)
	}
	reader := &retentionReader{}
	for _, value := range []time.Time{{}, time.Date(10000, 1, 1, 0, 0, 0, 0, time.UTC)} {
		result, err := host.InspectDeletionRetention(t.Context(), reader, &value)
		require.ErrorIs(t, err, host.ErrInvalidDiagnosticCutoff)
		require.Nil(t, result.Snapshot)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	result, err := host.InspectDeletionRetention(ctx, reader, nil)
	require.ErrorIs(t, err, context.Canceled)
	require.Nil(t, result.Snapshot)
	require.Zero(t, reader.calls)
}
