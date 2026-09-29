//go:build integration

package testshelpers_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	"github.com/assurrussa/gouploads/internal/testsupport"
)

func TestInitDB(t *testing.T) {
	ctx := context.Background()
	dbTestPath := testsupport.FindFileDir("testdata", testsupport.CallerCurrentFile())
	pgsql, db, cleanUp := testshelpers.PrepareDB(
		ctx,
		t,
		"tests-dbname",
		testshelpers.WithDatabasePathFilesMigration(dbTestPath),
		testshelpers.WithDatabaseFixedName(false),
		testshelpers.WithDatabaseVerbose(true),
		testshelpers.WithDatabaseLog(t.Logf),
	)
	require.NotNil(t, pgsql)
	require.NotNil(t, db)
	require.NotNil(t, cleanUp)
	assert.NotPanics(t, func() {
		cleanUp(ctx)
	})
}
