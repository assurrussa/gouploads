//go:build integration

package filerepo_test

import (
	"context"
	"errors"
	"io/fs"
	"strings"
	"testing"
	"time"

	"github.com/assurrussa/outbox/backends/pgsql/migrations"
	"github.com/assurrussa/outbox/backends/pgsql/storage/pgsqlclient"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func newJobEvidenceFixture(t *testing.T) (*pgsqlclient.Client, *host.FileRepo, *host.PostgresFileJobOutbox, host.File) {
	t.Helper()
	db, repo := newDiagnosticPostgres(t) // Owns and cleans up only a random schema.
	entries, err := fs.ReadDir(migrations.FS, ".")
	require.NoError(t, err)
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".sql") {
			continue
		}
		body, err := fs.ReadFile(migrations.FS, entry.Name())
		require.NoError(t, err)
		up, _, found := strings.Cut(string(body), "-- +goose Down")
		require.True(t, found)
		_, err = db.DB().Exec(t.Context(), "jobEvidence.ApplyOwnedMigration", up)
		require.NoError(t, err)
	}
	id := insertDiagnosticFile(t, db, `{"fileUploader":{"status":"queued"}}`, false)
	_, err = db.DB().Exec(t.Context(), "jobEvidence.BindOwnedFile",
		"update files set object_type = $2, object_id = 77, url = '' where id = $1", id, host.ObjectTypeKnowledgeBase.String())
	require.NoError(t, err)
	file, err := repo.GetByID(t.Context(), id)
	require.NoError(t, err)
	queue, err := host.NewPostgresFileJobOutbox(db)
	require.NoError(t, err)
	return db, repo, queue, file
}

func TestIntegrationDiagnosticsFileJobAtomicAssociationAndRollback(t *testing.T) {
	db, repo, queue, file := newJobEvidenceFixture(t)
	tx := transaction.New(db.DB())
	rollback := errors.New("synthetic rollback")
	err := tx.RunInTx(t.Context(), func(ctx context.Context) error {
		_, err := queue.PutFileJob(ctx, file, host.FileJobOriginalFinalization,
			"finalize_original_file", "synthetic-only", time.Now())
		if err != nil {
			return err
		}
		var sameTransaction bool
		err = db.DB().QueryRow(ctx, "jobEvidence.VerifySameTransaction",
			"select j.xmin = l.xmin from jobs j join file_job_associations l on l.job_id = j.id").Scan(&sameTransaction)
		require.NoError(t, err)
		require.True(t, sameTransaction)
		return rollback
	})
	require.ErrorIs(t, err, rollback)
	var count int
	require.NoError(t, db.DB().QueryRow(t.Context(), "jobEvidence.CountOwnedRows",
		"select (select count(*) from jobs) + (select count(*) from file_job_associations)").Scan(&count))
	require.Zero(t, count)
	result, err := host.InspectFileJobs(t.Context(), repo, file.ID, host.FileJobOriginalFinalization)
	require.NoError(t, err)
	require.Equal(t, "historical_unmapped", result.Coverage)

	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = queue.PutFileJob(ctx, file, host.FileJobOriginalFinalization, "finalize_original_file", "synthetic", time.Now())
	require.Error(t, err)
	require.NoError(t, db.DB().QueryRow(t.Context(), "jobEvidence.CountAfterCancellation",
		"select count(*) from jobs").Scan(&count))
	require.Zero(t, count)

	// Missing association schema must roll back the newly inserted queue job.
	_, err = db.DB().Exec(t.Context(), "jobEvidence.DropOwnedAssociationTable", "drop table file_job_associations")
	require.NoError(t, err)
	_, err = queue.PutFileJob(t.Context(), file, host.FileJobOriginalFinalization, "finalize_original_file", "synthetic", time.Now())
	require.Error(t, err)
	require.NoError(t, db.DB().QueryRow(t.Context(), "jobEvidence.CountAfterLinkFailure",
		"select count(*) from jobs").Scan(&count))
	require.Zero(t, count)
	_, err = host.InspectFileJobs(t.Context(), repo, file.ID, host.FileJobOriginalFinalization)
	require.ErrorIs(t, err, host.ErrDiagnosticUnavailable)
}

func TestIntegrationDiagnosticsFileJobStatesAndFailedOriginalIdentity(t *testing.T) {
	db, repo, queue, file := newJobEvidenceFixture(t)
	id, err := queue.PutFileJob(t.Context(), file, host.FileJobOriginalFinalization,
		"finalize_original_file", "private-payload-not-returned", time.Now().Add(-time.Minute))
	require.NoError(t, err)
	inspect := func(want host.FileJobState) host.FileJobEvidence {
		t.Helper()
		var result host.FileJobDiagnosis
		err := transaction.New(db.DB()).ReadCommitted(t.Context(), pgx.ReadOnly, func(ctx context.Context) error {
			var err error
			result, err = host.InspectFileJobs(ctx, repo, file.ID, host.FileJobOriginalFinalization)
			return err
		})
		require.NoError(t, err)
		require.Equal(t, "partial", result.Coverage)
		require.Equal(t, "unknown", result.Outcome)
		require.Equal(t, "unknown", result.Quiescence)
		require.Len(t, result.Jobs, 1)
		require.Equal(t, id.String(), result.Jobs[0].JobID)
		require.Equal(t, want, result.Jobs[0].State)
		require.False(t, result.ObservedAt.IsZero())
		require.Len(t, result.Jobs[0].Generation, 64)
		require.Len(t, result.Jobs[0].Revision, 64)
		return result.Jobs[0]
	}
	first := inspect(host.FileJobAvailable)
	require.Equal(t, "current", first.Binding)
	for _, tc := range []struct {
		sql   string
		state host.FileJobState
	}{
		{"update jobs set available_at = clock_timestamp() + interval '1 hour'", host.FileJobDelayed},
		{"update jobs set reserved_at = clock_timestamp() + interval '1 hour', lease_token = $1::uuid", host.FileJobLeased},
		{"update jobs set reserved_at = clock_timestamp() - interval '1 hour', lease_token = $1::uuid", host.FileJobLeaseExpired},
	} {
		args := []any{}
		if strings.Contains(tc.sql, "$1") {
			args = append(args, uuid.NewString())
		}
		_, err := db.DB().Exec(t.Context(), "jobEvidence.ChangeOwnedJob", tc.sql, args...)
		require.NoError(t, err)
		require.NotEqual(t, first.Revision, inspect(tc.state).Revision)
	}

	failedRowID := uuid.NewString()
	require.NotEqual(t, id.String(), failedRowID)
	_, err = db.DB().Exec(t.Context(), "jobEvidence.InsertOwnedFailure", `
insert into jobs_failed (id, job_id, connection, queue, name, schema_version, payload, reason, exception)
values ($1::uuid, $2, 'pgsql', 'queue', 'finalize_original_file', 1, 'private', 'private', 'private')`, failedRowID, id)
	require.NoError(t, err)
	inspect(host.FileJobUnknown) // Contradictory active + failed evidence.
	_, err = db.DB().Exec(t.Context(), "jobEvidence.RemoveOwnedActive", "delete from jobs where id = $1", id)
	require.NoError(t, err)
	require.Nil(t, inspect(host.FileJobFailed).Attempts)
	_, err = db.DB().Exec(t.Context(), "jobEvidence.RemoveOwnedFailure", "delete from jobs_failed where id = $1::uuid", failedRowID)
	require.NoError(t, err)
	inspect(host.FileJobUnknown) // Absence after retention never means success.

	_, err = db.DB().Exec(t.Context(), "jobEvidence.ChangeOwnedGeneration",
		"update files set slug = $2 where id = $1", file.ID, uuid.NewString())
	require.NoError(t, err)
	require.Equal(t, "mismatch", inspect(host.FileJobUnknown).Binding)
	_, err = queue.PutFileJob(t.Context(), file, host.FileJobDeletion, "deleted_file", "synthetic", time.Now())
	require.Error(t, err)
	_, err = db.DB().Exec(t.Context(), "jobEvidence.RemoveOwnedFile", "delete from files where id = $1", file.ID)
	require.NoError(t, err)
	require.Equal(t, "file_missing", inspect(host.FileJobUnknown).Binding)
}

func TestIntegrationDiagnosticsFileJobCoverageIsBoundedAndRepeatedPutsStayVisible(t *testing.T) {
	_, repo, queue, file := newJobEvidenceFixture(t)
	for range 101 {
		_, err := queue.PutFileJob(t.Context(), file, host.FileJobMediaAdmission,
			"send_resize_file", "synthetic-continuation", time.Now())
		require.NoError(t, err)
	}
	result, err := host.InspectFileJobs(t.Context(), repo, file.ID, host.FileJobMediaAdmission)
	require.NoError(t, err)
	require.Len(t, result.Jobs, 100)
	require.True(t, result.Truncated)
	require.Equal(t, "partial", result.Coverage)
	ids := map[string]bool{}
	for _, job := range result.Jobs {
		require.False(t, ids[job.JobID])
		ids[job.JobID] = true
	}
}

// Replaying a finalization key must not call the producer or create another link.
func TestIntegrationDiagnosticsFileJobFinalizationReplayPreservesOneAssociation(t *testing.T) {
	_, repo, queue, file := newJobEvidenceFixture(t)
	key, binding := uuid.NewString(), strings.Repeat("a", 64)
	calls := 0
	create := func(ctx context.Context) (host.File, error) {
		calls++
		_, err := queue.PutFileJob(ctx, file, host.FileJobOriginalFinalization,
			"finalize_original_file", "synthetic", time.Now())
		return file, err
	}
	for range 2 {
		got, err := repo.FinalizeUpload(t.Context(), key, binding, false, create)
		require.NoError(t, err)
		require.Equal(t, file.ID, got.ID)
	}
	require.Equal(t, 1, calls)
	_, err := repo.FinalizeUpload(t.Context(), key, strings.Repeat("b", 64), false, create)
	require.Error(t, err)
	require.Equal(t, 1, calls)
	result, err := host.InspectFileJobs(t.Context(), repo, file.ID, host.FileJobOriginalFinalization)
	require.NoError(t, err)
	require.Len(t, result.Jobs, 1)
}
