//go:build integration

package filerepo_test

import (
	"context"
	"fmt"
	"io/fs"
	"net"
	"strconv"
	"strings"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	pgsqlclient "github.com/assurrussa/outbox/backends/pgsql/storage/pgsqlclient"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"

	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	"github.com/assurrussa/gouploads/host"
)

// This fixture owns exactly one random schema. It never resets a shared database
// or runs migration downs, including when a test leaves a pending deletion plan.
func newDiagnosticPostgres(t *testing.T) (*pgsqlclient.Client, *host.FileRepo) {
	t.Helper()
	cfg := testshelpers.Config
	address, port := cfg.PostgresAddress, cfg.PostgresPort
	if cfg.PostgresAddressLocal != "" {
		address = cfg.PostgresAddressLocal
	}
	if cfg.PostgresPortLocal > 0 {
		port = cfg.PostgresPortLocal
	}
	options := pgsqlclient.NewOptions(net.JoinHostPort(address, strconv.Itoa(port)),
		cfg.PostgresUser, cfg.PostgresPassword, cfg.PostgresDatabase,
		pgsqlclient.WithLogger(logger.Discard()), pgsqlclient.WithSSLMode(cfg.PostgresSSLMode),
		pgsqlclient.WithMinConnectionsCount(1), pgsqlclient.WithMaxConnectionsCount(1))
	admin, err := pgsqlclient.NewPool(t.Context(), options)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, admin.Close()) })

	schema := "gouploads_diagnostics_" + strings.ReplaceAll(uuid.NewString(), "-", "")
	identifier := pgx.Identifier{schema}.Sanitize()
	_, err = admin.DB().Exec(t.Context(), "diagnostics.CreateOwnedSchema", "create schema "+identifier)
	require.NoError(t, err)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		_, err := admin.DB().Exec(ctx, "diagnostics.DropOwnedSchema", "drop schema "+identifier+" cascade")
		require.NoError(t, err)
	})

	pgsqlclient.WithRuntimeParams(map[string]string{"search_path": schema})(&options)
	database, err := pgsqlclient.NewPool(t.Context(), options)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, database.Close()) })
	var actualSchema string
	require.NoError(t, database.DB().QueryRow(t.Context(), "diagnostics.VerifyOwnedSchema",
		"select current_schema()").Scan(&actualSchema))
	require.Equal(t, schema, actualSchema)

	migrationFS, err := host.MigrationsFS()
	require.NoError(t, err)
	files, err := host.MigrationFiles()
	require.NoError(t, err)
	for _, name := range files {
		body, err := fs.ReadFile(migrationFS, name)
		require.NoError(t, err)
		up, _, found := strings.Cut(string(body), "-- +goose Down")
		require.True(t, found, "fixture expects SQL up/down migrations")
		_, err = database.DB().Exec(t.Context(), "diagnostics.ApplyOwnedMigration", up)
		require.NoError(t, err)
	}
	repo, err := host.NewFileRepo(database, transaction.New(database.DB()))
	require.NoError(t, err)
	return database, repo
}

func insertDiagnosticFile(t *testing.T, database *pgsqlclient.Client, data any, deleted bool) int64 {
	t.Helper()
	var id int64
	require.NoError(t, database.DB().QueryRow(t.Context(), "diagnostics.InsertOwnedFile", `
insert into files (original_filename, filename, slug, data, deleted_at)
values ('diagnostic.png', 'diagnostic.png', $1, $2::jsonb,
        case when $3 then clock_timestamp() else null end)
returning id`, uuid.NewString(), data, deleted).Scan(&id))
	return id
}

func TestIntegrationDiagnosticsMetadataAndSoftDeletion(t *testing.T) {
	database, repo := newDiagnosticPostgres(t)
	cases := []struct {
		name    string
		data    any
		deleted bool
		status  host.FileUploadTaskStatus
		presets bool
		upload  host.DiagnosticState
	}{
		{name: "SQL NULL", data: nil, upload: host.DiagnosticUnknown},
		{name: "JSON null", data: `null`, upload: host.DiagnosticUnknown},
		{name: "empty object", data: `{}`, upload: host.DiagnosticUnknown},
		{name: "empty presets", data: `{"presets":{}}`, upload: host.DiagnosticUnknown},
		{name: "null presets", data: `{"presets":null}`, upload: host.DiagnosticUnknown},
		{name: "array presets", data: `{"presets":[{"name":"main"}]}`, upload: host.DiagnosticUnknown},
		{name: "string presets", data: `{"presets":"private/path"}`, upload: host.DiagnosticUnknown},
		{name: "number presets", data: `{"presets":12}`, upload: host.DiagnosticUnknown},
		{name: "legacy completion", data: `{"presets":{"main":{}}}`, presets: true, upload: host.DiagnosticCompleted},
		{
			name: "queued", data: `{"fileUploader":{"status":"queued"}}`,
			status: host.FileUploadTaskStatusQueued, upload: host.DiagnosticQueued,
		},
		{
			name: "processing", data: `{"fileUploader":{"status":"processing"}}`,
			status: host.FileUploadTaskStatusProcessing, upload: host.DiagnosticProcessing,
		},
		{
			name: "completed", data: `{"fileUploader":{"status":"completed"}}`,
			status: host.FileUploadTaskStatusCompleted, upload: host.DiagnosticCompleted,
		},
		{
			name: "failed overrides presets", data: `{"fileUploader":{"status":"failed"},"presets":{"main":{}}}`,
			status: host.FileUploadTaskStatusFailed, presets: true, upload: host.DiagnosticFailed,
		},
		{
			name: "future overrides presets", data: `{"fileUploader":{"status":"private/path"},"presets":{"main":{}}}`,
			status: "unknown", presets: true, upload: host.DiagnosticUnknown,
		},
		{
			name: "soft deleted", data: `{"fileUploader":{"status":"completed"}}`, deleted: true,
			status: host.FileUploadTaskStatusCompleted, upload: host.DiagnosticCompleted,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			id := insertDiagnosticFile(t, database, tc.data, tc.deleted)
			snapshot, err := repo.GetFileLifecycle(t.Context(), id)
			require.NoError(t, err)
			require.Equal(t, host.FileLifecycleSnapshot{
				FileExists: true, FileDeleted: tc.deleted, UploadStatus: tc.status, HasPresets: tc.presets,
			}, snapshot)
			result, err := host.DiagnoseFile(t.Context(), repo, id)
			require.NoError(t, err)
			fileState := host.DiagnosticPresent
			if tc.deleted {
				fileState = host.DiagnosticDeleted
			}
			require.Equal(t, host.FileDiagnosis{
				FileID: id, File: fileState, Upload: tc.upload,
				Finalization: host.DiagnosticNone, Deletion: host.DiagnosticNone, Job: host.DiagnosticUnavailable,
			}, result)
		})
	}
}

func TestIntegrationDiagnosticsMissingFileAndRetainedEvidence(t *testing.T) {
	database, repo := newDiagnosticPostgres(t)
	// IDs 1001/1002 are owned solely by this schema; neither has a file row.
	for _, completed := range []bool{false, true} {
		t.Run(fmt.Sprintf("completed=%t", completed), func(t *testing.T) {
			id := int64(1001)
			deletion := host.DiagnosticDeleting
			if completed {
				id = 1002
				deletion = host.DiagnosticCompleted
			}
			_, err := database.DB().Exec(t.Context(), "diagnostics.InsertOwnedDeletion", `
insert into file_deletions (file_id, payload, completed_at)
values ($1, '{"private":"path-and-payload-must-not-be-decoded"}',
        case when $2 then clock_timestamp() else null end)`, id, completed)
			require.NoError(t, err)
			// More than one finalization key must still produce exactly one observation.
			for range 2 {
				_, err = database.DB().Exec(t.Context(), "diagnostics.InsertOwnedFinalization", `
insert into upload_finalizations (finalization_key, binding_hash, file_id)
values ($1::uuid, $2, $3)`, uuid.NewString(), strings.Repeat("a", 64), id)
				require.NoError(t, err)
			}
			snapshot, err := repo.GetFileLifecycle(t.Context(), id)
			require.NoError(t, err)
			require.Equal(t, host.FileLifecycleSnapshot{
				FinalizationRecorded: true,
				DeletionPlanned:      true, DeletionCompleted: completed,
			}, snapshot)
			result, err := host.DiagnoseFile(t.Context(), repo, id)
			require.NoError(t, err)
			require.Equal(t, host.FileDiagnosis{
				FileID: id, File: host.DiagnosticMissing, Upload: host.DiagnosticUnknown,
				Finalization: host.DiagnosticRecorded, Deletion: deletion, Job: host.DiagnosticUnavailable,
			}, result)
		})
	}
	result, err := host.DiagnoseFile(t.Context(), repo, 1003)
	require.NoError(t, err)
	require.Equal(t, host.FileDiagnosis{
		FileID: 1003, File: host.DiagnosticMissing, Upload: host.DiagnosticUnknown,
		Finalization: host.DiagnosticNone, Deletion: host.DiagnosticNone, Job: host.DiagnosticUnavailable,
	}, result)
}

func TestIntegrationDiagnosticsMissingLifecycleTableIsUnavailable(t *testing.T) {
	for _, table := range []string{"upload_finalizations", "file_deletions"} {
		t.Run(table, func(t *testing.T) {
			database, repo := newDiagnosticPostgres(t)
			id := insertDiagnosticFile(t, database, `{"fileUploader":{"status":"queued"}}`, false)
			// Drop only a table in this fixture's random schema, never a shared table.
			_, err := database.DB().Exec(t.Context(), "diagnostics.DropOwnedLifecycleTable",
				"drop table "+pgx.Identifier{table}.Sanitize())
			require.NoError(t, err)
			snapshot, err := repo.GetFileLifecycle(t.Context(), id)
			require.Error(t, err)
			require.Equal(t, host.FileLifecycleSnapshot{}, snapshot)
			result, err := host.DiagnoseFile(t.Context(), repo, id)
			require.ErrorIs(t, err, host.ErrDiagnosticUnavailable)
			require.Equal(t, host.ErrDiagnosticUnavailable.Error(), err.Error())
			require.NotContains(t, err.Error(), table)
			require.Equal(t, host.FileDiagnosis{
				FileID: id, File: host.DiagnosticUnavailable, Upload: host.DiagnosticUnavailable,
				Finalization: host.DiagnosticUnavailable, Deletion: host.DiagnosticUnavailable, Job: host.DiagnosticUnavailable,
			}, result)
		})
	}
}
