//go:build integration

package filerepo_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestIntegrationDiagnosticsDeletionRetentionInventory(t *testing.T) {
	database, repo := newDiagnosticPostgres(t)
	empty, err := host.InspectDeletionRetention(t.Context(), repo, nil)
	require.NoError(t, err)
	require.Equal(t, host.DiagnosticPresent, empty.Source)
	require.Zero(t, empty.Snapshot.TotalCount)
	require.Nil(t, empty.Snapshot.OldestPendingCreatedAt)
	require.Nil(t, empty.Snapshot.OldestCompletedAt)
	require.Nil(t, empty.Snapshot.Projection)
	cutoff := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	const created = "2026-01-01T00:00:00Z"
	cases := []struct {
		payload, createdAt                               string
		completedAt                                      any
		malformed, unknownBinding, unknownAge, projected bool
	}{
		{
			payload:   `{"file":{"id":1000,"objectType":"admin","objectId":123},"paths":["private/path"],"userId":"private-actor"}`,
			createdAt: created,
		},
		{`{"file":{"id":1001,"objectType":"custom_host","objectId":123}}`, created, "2026-01-02T00:00:00Z", false, false, false, true},
		{`{"file":{"id":1002,"objectType":"admin","objectId":123}}`, created, cutoff, false, false, false, false},
		{`{"file":{"id":1003,"objectType":"admin","objectId":123}}`, created, "2026-01-04T00:00:00Z", false, false, false, false},
		{`null`, created, "2026-01-02T00:00:00Z", true, false, false, true},
		{`{"futureFormat":2}`, created, nil, true, false, false, false},
		{`{"file":{"id":9999}}`, created, nil, true, false, false, false},
		{`{"file":{"id":1007}}`, created, "2026-01-02T00:00:00Z", false, true, false, true},
		{`{"file":{"id":1008,"objectType":"PRIVATE/PATH","objectId":123}}`, created, nil, false, true, false, false},
		{`{"file":{"id":1009,"objectType":"admin","objectId":9223372036854775808}}`, created, nil, false, true, false, false},
		{`{"file":{"id":1010,"objectType":"admin","objectId":123}}`, created, "infinity", false, false, true, false},
		{`{"file":{"id":1011}}`, "infinity", nil, false, true, true, false},
		{`{"file":{"id":1012}}`, created, "2025-12-31T00:00:00Z", false, true, true, false},
		{`{"file":{"id":1013}}`, created, "9999-01-01T00:00:00Z", false, true, true, false},
		{`{"file":{"id":1014}}`, "0001-01-01 00:00:00 BC", nil, false, true, true, false},
	}
	var totalBytes, pendingBytes, completedBytes, projectedBytes int64
	var pending, completed, malformed, unknownBinding, unknownAge, projected int64
	for i, tc := range cases {
		var text string
		require.NoError(t, database.DB().QueryRow(t.Context(), "retention.InsertOwnedPlan", `
insert into file_deletions(file_id, payload, created_at, completed_at)
values ($1, $2::jsonb, $3::timestamptz, $4::timestamptz) returning payload::text`,
			int64(1000+i), tc.payload, tc.createdAt, tc.completedAt).Scan(&text))
		bytes := int64(len(text))
		totalBytes += bytes
		if tc.completedAt == nil {
			pending++
			pendingBytes += bytes
		} else {
			completed++
			completedBytes += bytes
		}
		if tc.malformed {
			malformed++
		}
		if tc.unknownBinding {
			unknownBinding++
		}
		if tc.unknownAge {
			unknownAge++
		}
		if tc.projected {
			projected++
			projectedBytes += bytes
		}
	}
	// Capture the exact persisted evidence before/after. Other tombstones remain
	// in the same owned schema and must also survive the inventory unchanged.
	_, err = database.DB().Exec(t.Context(), "retention.InsertOwnedFinalization", `
insert into upload_finalizations(finalization_key, binding_hash, file_id)
values ('00000000-0000-4000-8000-000000000001', repeat('a', 64), 1001)`)
	require.NoError(t, err)
	const persisted = `select jsonb_build_object(
 'plans', (select jsonb_agg(to_jsonb(d) order by file_id) from file_deletions d),
 'finalizations', (select jsonb_agg(to_jsonb(f) order by finalization_key) from upload_finalizations f),
 'files', (select jsonb_agg(to_jsonb(f) order by id) from files f))::text`
	var before, after string
	require.NoError(t, database.DB().QueryRow(t.Context(), "retention.Before", persisted).Scan(&before))
	// The fixture's pool has exactly one connection. Read-only session mode makes
	// any write/row lock in these calls fail, including on other lifecycle tables.
	_, err = database.DB().Exec(t.Context(), "retention.ReadOnlySession", "set default_transaction_read_only = on")
	require.NoError(t, err)
	var readOnly string
	require.NoError(t, database.DB().QueryRow(t.Context(), "retention.VerifyReadOnlySession",
		"show default_transaction_read_only").Scan(&readOnly))
	require.Equal(t, "on", readOnly)
	result, err := host.InspectDeletionRetention(t.Context(), repo, &cutoff)
	require.NoError(t, err)
	snapshot := result.Snapshot
	require.Equal(t, host.DiagnosticPresent, result.Source)
	require.Equal(t, int64(len(cases)), snapshot.TotalCount)
	require.Equal(t, pending, snapshot.PendingCount)
	require.Equal(t, completed, snapshot.CompletedCount)
	require.Equal(t, malformed, snapshot.MalformedEnvelopeCount)
	require.Equal(t, unknownBinding, snapshot.UnrecognizedBindingCount)
	require.Equal(t, unknownAge, snapshot.UnknownAgeCount)
	require.Equal(t, totalBytes, snapshot.PayloadJSONTextBytesEstimate)
	require.Equal(t, pendingBytes, snapshot.PendingPayloadJSONTextBytesEstimate)
	require.Equal(t, completedBytes, snapshot.CompletedPayloadJSONTextBytesEstimate)
	require.Equal(t, cutoff.Add(-48*time.Hour), *snapshot.OldestPendingCreatedAt)
	require.Equal(t, cutoff.Add(-24*time.Hour), *snapshot.OldestCompletedAt)
	require.Equal(t, &host.DeletionRetentionProjection{
		CompletedBefore: cutoff, CompletedCount: projected,
		PayloadJSONTextBytesEstimate: projectedBytes,
	}, snapshot.Projection)
	require.False(t, snapshot.ObservedAt.IsZero())
	withoutCutoff, err := host.InspectDeletionRetention(t.Context(), repo, nil)
	require.NoError(t, err)
	require.Nil(t, withoutCutoff.Snapshot.Projection)
	require.NoError(t, database.DB().QueryRow(t.Context(), "retention.After", persisted).Scan(&after))
	require.Equal(t, before, after)
	body, err := json.Marshal(result)
	require.NoError(t, err)
	for _, private := range []string{"private/path", "private-actor", "00000000-0000-4000", "admin", "custom_host"} {
		require.NotContains(t, string(body), private)
	}
}

func TestIntegrationDiagnosticsDeletionRetentionMissingTable(t *testing.T) {
	database, repo := newDiagnosticPostgres(t)
	_, err := database.DB().Exec(t.Context(), "retention.DropOwnedTable", "drop table file_deletions")
	require.NoError(t, err)
	result, err := host.InspectDeletionRetention(t.Context(), repo, nil)
	require.ErrorIs(t, err, host.ErrDiagnosticUnavailable)
	require.Equal(t, host.DeletionRetentionInventory{Source: host.DiagnosticUnavailable}, result)
	require.Equal(t, host.ErrDiagnosticUnavailable.Error(), err.Error())
}

func TestIntegrationDiagnosticsCompletedMinimalTombstoneDecoder(t *testing.T) {
	database, repo := newDiagnosticPostgres(t)
	_, err := database.DB().Exec(t.Context(), "retention.InsertOwnedMinimalTombstone", `
insert into file_deletions(file_id, payload, completed_at)
values (123, '{"file":{"id":123,"objectType":"admin","objectId":456}}', clock_timestamp())`)
	require.NoError(t, err)
	require.NoError(t, transaction.New(database.DB()).RunInTx(t.Context(), func(ctx context.Context) error {
		plan, found, err := repo.GetDeletionPlanForUpdate(ctx, 123)
		require.NoError(t, err)
		require.True(t, found)
		require.True(t, plan.Completed)
		require.EqualValues(t, 123, plan.File.ID)
		require.Equal(t, host.ObjectTypeAdmin, plan.File.ObjectType)
		require.NotNil(t, plan.File.ObjectID)
		require.EqualValues(t, 456, *plan.File.ObjectID)
		require.Empty(t, plan.Paths)
		require.Empty(t, plan.AfterEvents)
		return nil
	}))
}

func TestIntegrationDiagnosticsDeletionRetentionNanosecondCutoff(t *testing.T) {
	database, repo := newDiagnosticPostgres(t)
	anchor := time.Date(2026, 1, 2, 0, 0, 0, 123456000, time.UTC)
	var payloadText string
	require.NoError(t, database.DB().QueryRow(t.Context(), "retention.InsertOwnedCutoffBoundary", `
insert into file_deletions(file_id, payload, created_at, completed_at)
values (123, '{"file":{"id":123,"objectType":"admin","objectId":456}}', $1, $2)
returning payload::text`, anchor.Add(-time.Hour), anchor).Scan(&payloadText))
	_, err := database.DB().Exec(t.Context(), "retention.InsertOwnedNextMicrosecond", `
insert into file_deletions(file_id, payload, created_at, completed_at)
values (124, '{"file":{"id":124,"objectType":"admin","objectId":456}}', $1, $2)`,
		anchor.Add(-time.Hour), anchor.Add(time.Microsecond))
	require.NoError(t, err)
	var before, after string
	const persisted = "select jsonb_agg(to_jsonb(d) order by file_id)::text from file_deletions d"
	require.NoError(t, database.DB().QueryRow(t.Context(), "retention.CutoffBefore", persisted).Scan(&before))
	_, err = database.DB().Exec(t.Context(), "retention.CutoffReadOnly", "set default_transaction_read_only = on")
	require.NoError(t, err)
	for _, tc := range []struct {
		name   string
		cutoff time.Time
		count  int64
	}{
		{"T minus 1ns", anchor.Add(-time.Nanosecond), 0},
		{"T", anchor, 0},
		{"T plus 1ns", anchor.Add(time.Nanosecond), 1},
		{"T plus 1us minus 1ns", anchor.Add(time.Microsecond - time.Nanosecond), 1},
		{"T plus 1us", anchor.Add(time.Microsecond), 1},
		{"T plus 1us plus 1ns", anchor.Add(time.Microsecond + time.Nanosecond), 2},
		{"T plus 1ns with offset", anchor.Add(time.Nanosecond).In(time.FixedZone("fixture", 9*3600)), 1},
		{"before Unix epoch", time.Unix(-1, 1), 0},
		{"UTC year boundary", time.Date(9999, 12, 31, 23, 59, 59, 999999999, time.UTC), 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			original := tc.cutoff
			result, err := host.InspectDeletionRetention(t.Context(), repo, &tc.cutoff)
			require.NoError(t, err)
			require.Equal(t, host.DiagnosticPresent, result.Source)
			require.Equal(t, original.UTC(), result.Snapshot.Projection.CompletedBefore)
			require.Equal(t, original, tc.cutoff)
			require.Equal(t, tc.count, result.Snapshot.Projection.CompletedCount)
			require.Equal(t, tc.count*int64(len(payloadText)), result.Snapshot.Projection.PayloadJSONTextBytesEstimate)
		})
	}
	require.NoError(t, database.DB().QueryRow(t.Context(), "retention.CutoffAfter", persisted).Scan(&after))
	require.Equal(t, before, after)
}
