//go:build integration

package filerepo_test

import (
	"context"
	"io/fs"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func lifecycleMigrationSQL(t *testing.T) (up, down string) {
	t.Helper()
	migrations, err := host.MigrationsFS()
	require.NoError(t, err)
	source, err := fs.ReadFile(migrations, "20260930120000_upload_lifecycle_safety.sql")
	require.NoError(t, err)
	up, down, found := strings.Cut(string(source), "-- +goose Down")
	require.True(t, found)
	return up, down
}

func TestIntegrationHardeningMigrationRejectsDuplicatePrimary(t *testing.T) {
	ctx, _, ts := NewTestRepoSuite(t)
	defer ts.cleanUp(ctx)
	up, down := lifecycleMigrationSQL(t)
	err := ts.trx.RunInTx(ctx, func(txCtx context.Context) error {
		if _, err := ts.db.DB().Exec(txCtx, "rollback_lifecycle_schema", down); err != nil {
			return err
		}
		const duplicates = `insert into files
			(object_type, object_id, original_filename, filename, slug, is_primary)
			values ('exercise', 9123, 'a.png', 'a.png', 'primary-a', true),
			       ('exercise', 9123, 'b.png', 'b.png', 'primary-b', true)`
		if _, err := ts.db.DB().Exec(txCtx, "seed_duplicate_primary", duplicates); err != nil {
			return err
		}
		_, err := ts.db.DB().Exec(txCtx, "apply_lifecycle_schema", up)
		return err
	})
	require.ErrorContains(t, err, "duplicate primary files")
	// The failed migration must roll back every preceding DDL statement too.
	var restored bool
	query := `select to_regclass('upload_finalizations') is not null
		and to_regclass('files_primary_unique_idx') is not null
		and not exists(select 1 from files where object_id = 9123)`
	require.NoError(t, ts.db.DB().QueryRow(ctx, "check_migration_rollback", query).Scan(&restored))
	require.True(t, restored)
}

func TestIntegrationHardeningMigrationRejectsPendingRollback(t *testing.T) {
	for _, pending := range []struct {
		name string
		sql  string
	}{
		{"deletion", "insert into file_deletions (file_id, payload) values (9123, '{}'::jsonb)"},
		{"cleaning", `insert into upload_sessions
			(id, upload_length, object_path, original_name, file_name, owner_uuid,
			 multipart_upload_id, finalization_key, expires_at, status)
			values ('e6c2bc7d-332c-47b9-b55b-d7ed3b20c5ed', 1, 'probe/source.png',
			 'source.png', 'source.png', 'e6c2bc7d-332c-47b9-b55b-d7ed3b20c5ed',
			 'probe', '412f021b-e8ac-4536-a371-61a37d3b1262', now(), 'cleaning')`},
	} {
		t.Run(pending.name, func(t *testing.T) {
			ctx, _, ts := NewTestRepoSuite(t)
			defer ts.cleanUp(ctx)
			_, down := lifecycleMigrationSQL(t)
			err := ts.trx.RunInTx(ctx, func(txCtx context.Context) error {
				if _, err := ts.db.DB().Exec(txCtx, "seed_pending_operation", pending.sql); err != nil {
					return err
				}
				_, err := ts.db.DB().Exec(txCtx, "rollback_pending_lifecycle", down)
				return err
			})
			require.ErrorContains(t, err, "drain pending cleanup/deletion operations")
			var restored bool
			require.NoError(t, ts.db.DB().QueryRow(ctx, "check_guarded_schema",
				"select to_regclass('upload_finalizations') is not null and to_regclass('file_deletions') is not null",
			).Scan(&restored))
			require.True(t, restored)
		})
	}
}
