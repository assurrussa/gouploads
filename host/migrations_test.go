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
	require.Equal(t, []string{"20250705111436_create_files_table.sql"}, files)

	migrationFS, err := host.MigrationsFS()
	require.NoError(t, err)

	data, err := fs.ReadFile(migrationFS, files[0])
	require.NoError(t, err)
	require.Contains(t, string(data), "create table if not exists files")
}

func TestEmbeddedMigrationMatchesCanonicalMigrationFile(t *testing.T) {
	const filename = "20250705111436_create_files_table.sql"

	canonical, err := os.ReadFile(filepath.Join("..", "db", "migrations", filename))
	require.NoError(t, err)

	migrationFS, err := host.MigrationsFS()
	require.NoError(t, err)

	embedded, err := fs.ReadFile(migrationFS, filename)
	require.NoError(t, err)
	require.Equal(t, string(canonical), string(embedded))
}
