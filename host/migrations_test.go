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
	require.Equal(t, []string{
		"20250705111436_create_files_table.sql",
		"20260710120000_create_upload_sessions.sql",
	}, files)

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
	require.Contains(t, string(data), "create table if not exists upload_sessions")
	require.Contains(t, string(data), "revision")
	require.Contains(t, string(data), "lease_owner")
	require.Contains(t, string(data), "finalization_key")
	require.Contains(t, string(data), "expires_at")
}

func TestEmbeddedMigrationMatchesCanonicalMigrationFile(t *testing.T) {
	t.Parallel()

	for _, filename := range []string{
		"20250705111436_create_files_table.sql",
		"20260710120000_create_upload_sessions.sql",
	} {
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
