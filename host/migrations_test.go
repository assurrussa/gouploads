package host_test

import (
	"io/fs"
	"os"
	"path/filepath"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
)

func TestMigrationsFSExposesFilesTableMigration(t *testing.T) {
	files, err := host.MigrationFiles()
	require.NoError(t, err)
	require.Equal(t,
		[]string{
			"20250705111436_create_files_table.sql",
			"20260710120000_create_upload_sessions.sql",
			"20260930120000_upload_lifecycle_safety.sql",
			"20261008120000_file_job_associations.sql",
		},
		files)
	migrationFS, err := host.MigrationsFS()
	require.NoError(t, err)
	data, err := fs.ReadFile(migrationFS, files[0])
	require.NoError(t, err)
	require.Contains(t, string(data), "create table if not exists files")
}

func TestUploadSessionsMigrationUsesDurableFencingState(t *testing.T) {
	migrationFS, err := host.MigrationsFS()
	require.NoError(t, err)
	data, err := fs.ReadFile(migrationFS, "20260710120000_create_upload_sessions.sql")
	require.NoError(t, err)
	for _, field := range []string{
		"create table if not exists upload_sessions",
		"revision",
		"lease_owner",
		"finalization_key",
		"expires_at",
	} {
		require.Contains(t, string(data), field)
	}
}

func TestEmbeddedMigrationMatchesCanonicalMigrationFile(t *testing.T) {
	t.Parallel()
	files, err := host.MigrationFiles()
	require.NoError(t, err)
	for _, filename := range files {
		t.Run(filename, func(t *testing.T) {
			t.Parallel()
			canonical, err := os.ReadFile(filepath.Join("..", "db", "migrations", filename))
			require.NoError(t, err)
			migrationFS, err := host.MigrationsFS()
			require.NoError(t, err)
			embedded, err := fs.ReadFile(migrationFS, filename)
			require.NoError(t, err)
			require.Equal(t, string(canonical), string(embedded))
		})
	}
}

func TestSafetyMigrationIncludesDurableHandoffAndDeletion(t *testing.T) {
	migrationFS, err := host.MigrationsFS()
	require.NoError(t, err)
	data, err := fs.ReadFile(migrationFS, "20260930120000_upload_lifecycle_safety.sql")
	require.NoError(t, err)
	for _, field := range []string{
		"upload_finalizations",
		"binding_hash",
		"file_deletions",
		"cleaning",
		"files_primary_unique_idx",
	} {
		require.Contains(t, string(data), field)
	}
}
