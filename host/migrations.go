package host

import (
	"embed"
	"io/fs"
	"slices"
)

//go:embed migrations/*.sql
var migrations embed.FS

// MigrationsFS returns the embedded goose migrations owned by gouploads.
//
// Host applications may copy these migrations into their own migration tree or
// mount this filesystem directly when their migration runner supports fs.FS.
// Outbox storage migrations remain owned by the outbox backend package.
func MigrationsFS() (fs.FS, error) {
	return fs.Sub(migrations, "migrations")
}

// MigrationFiles returns the stable sorted list of embedded migration files.
func MigrationFiles() ([]string, error) {
	migrationFS, err := MigrationsFS()
	if err != nil {
		return nil, err
	}

	entries, err := fs.ReadDir(migrationFS, ".")
	if err != nil {
		return nil, err
	}

	files := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			continue
		}
		files = append(files, entry.Name())
	}
	slices.Sort(files)

	return files, nil
}
