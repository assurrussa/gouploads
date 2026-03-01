//go:build integration

package testshelpers

import (
	"context"
	"fmt"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/assurrussa/goshared/pkg/logger"
	"github.com/assurrussa/outbox/infrastructure/pgsql/migrator"
	pgsqlpgx "github.com/assurrussa/outbox/infrastructure/pgsql/storage"
	pgsqlclient "github.com/assurrussa/outbox/infrastructure/pgsql/storage/pgsqlclient"
	"github.com/assurrussa/outbox/infrastructure/pgsql/storage/pgsqlinit"
	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

var (
	runRateLimitCh = make(chan struct{}, 3) // Максимальное кол-во паралельно запускаемых тестов
	migrationLock  sync.Mutex               // Для миграций
)

type OptionDatabase func(*OptionsDatabase)

type OptionsDatabase struct {
	pathFilesMigration []string
	fixedDBName        bool
	verbose            bool
	log                func(string, ...any)
}

func WithDatabasePathFilesMigration(paths ...string) OptionDatabase {
	return func(o *OptionsDatabase) {
		o.pathFilesMigration = paths
	}
}

func WithDatabaseFixedName(isFixed bool) OptionDatabase {
	return func(o *OptionsDatabase) {
		o.fixedDBName = isFixed
	}
}

func WithDatabaseVerbose(verbose bool) OptionDatabase {
	return func(o *OptionsDatabase) {
		o.verbose = verbose
	}
}

func WithDatabaseLog(log func(string, ...any)) OptionDatabase {
	return func(o *OptionsDatabase) {
		o.log = log
	}
}

func PrepareDB(
	ctx context.Context, t *testing.T, dbName string, opts ...OptionDatabase,
) (pgsql *pgsqlclient.Client, db *DBHelper, cleanUp func(ctx context.Context)) {
	t.Helper()
	require.NotEmpty(t, dbName)

	select {
	case <-ctx.Done():
		t.Logf("Warning: context done: %v", ctx.Err())
		return nil, nil, cleanUp
	case runRateLimitCh <- struct{}{}:
	}
	t.Cleanup(func() {
		<-runRateLimitCh
	})

	options := &OptionsDatabase{
		pathFilesMigration: []string{"db/migrations"},
		log:                t.Logf,
		fixedDBName:        false,
		verbose:            true,
	}

	for _, o := range opts {
		o(options)
	}

	if !options.fixedDBName {
		dbName += strings.ReplaceAll(uuid.New().String(), "-", "")
	}
	options.log("database: %s", dbName)

	lg := CreateLogger(t).WithNamed(dbName)

	port := Config.PostgresPort
	if Config.PostgresPortLocal > 0 {
		port = Config.PostgresPortLocal
	}
	address := Config.PostgresAddress
	if Config.PostgresAddressLocal != "" {
		address = Config.PostgresAddressLocal
	}
	cfg := pgsqlpgx.PSQLConfig{
		Address:             address + ":" + strconv.Itoa(port),
		Username:            Config.PostgresUser,
		Password:            Config.PostgresPassword,
		Database:            Config.PostgresDatabase,
		SSLMode:             Config.PostgresSSLMode,
		DebugMode:           Config.PostgresDebug,
		MinConnectionsCount: Config.MinConnectionsCount,
		MaxConnectionsCount: Config.MaxConnectionsCount,
		MaxConnIdleTime:     Config.MaxConnIdleTime,
		MaxConnLifeTime:     Config.MaxConnLifeTime,
	}
	poolMain, err := pgsqlinit.CreateWithConfig(ctx, cfg, pgsqlclient.WithEnvironment(Config.Env), pgsqlclient.WithLogger(lg))
	require.NoError(t, err)
	require.NoError(t, createDatabase(ctx, dbName, poolMain))

	cfg.Database = dbName
	pool, err := pgsqlinit.CreateWithConfig(ctx, cfg, pgsqlclient.WithEnvironment(Config.Env), pgsqlclient.WithLogger(lg))
	require.NoError(t, err)
	migrationDatabase(t, ctx, cfg, pool, "up", options, lg)

	db = &DBHelper{T: t, DB: pool}

	return pool, db, func(ctx context.Context) {
		migrationDatabase(t, ctx, cfg, pool, "reset", options, lg)

		assert.NoError(t, pool.Close())
		assert.NoError(t, dropDatabaseIfExists(ctx, dbName, poolMain))
		assert.NoError(t, poolMain.Close())
	}
}

func migrationDatabase(
	t *testing.T,
	ctx context.Context,
	cfg pgsqlpgx.PSQLConfig,
	pool *pgsqlclient.Client,
	command string,
	options *OptionsDatabase,
	lg logger.Logger,
) {
	t.Helper()
	migrationLock.Lock()
	defer migrationLock.Unlock()

	var err error
	pgxPool, okPgxPool := pool.DB().(pgsqlpgx.DBPgxEnginePool)
	if !okPgxPool {
		t.Fatalf("pgsqlpgx.DBPgxEnginePool is not DB pool")
	}
	sqlDB := stdlib.OpenDBFromPool(pgxPool.Pool())
	// Ensure we close the database/sql wrapper to avoid leaking the
	// connectionOpener goroutine detected by goleak.
	// Close only the wrapper; the underlying pgx pool remains managed by pool.
	defer func() { assert.NoError(t, sqlDB.Close()) }()

	// NOTE: Schema migrationDatabase is not thread-safe :(
	for _, path := range options.pathFilesMigration {
		dir := strings.Replace(path, Config.BasePath, "", 1)
		dir = filepath.Join(Config.BasePath, dir)

		err = migrator.Run(
			ctx,
			sqlDB,
			lg,
			migrator.WithCommand(command),
			migrator.WithDirectory(dir),
			migrator.WithArgs(),
		)
		require.NoError(t, err)
	}
}

func createDatabase(ctx context.Context, dbName string, pool *pgsqlclient.Client) error {
	if err := dropDatabaseIfExists(ctx, dbName, pool); err != nil {
		return fmt.Errorf("drop db %s: %v", dbName, err)
	}

	if _, err := pool.DB().Exec(ctx, "createDatabase", fmt.Sprintf("CREATE DATABASE %q", dbName)); err != nil {
		return fmt.Errorf("create db %s: %v", dbName, err)
	}

	return nil
}

func dropDatabaseIfExists(ctx context.Context, dbName string, pool *pgsqlclient.Client) error {
	_, err := pool.DB().Exec(ctx, "dropDatabase", fmt.Sprintf("DROP DATABASE IF EXISTS %q", dbName))
	return err
}

type DBHelper struct {
	T  *testing.T
	DB *pgsqlclient.Client
}

func (db *DBHelper) CreateTable(ctx context.Context, tableName string, sql string) {
	db.T.Helper()
	_, err := db.DB.DB().Exec(ctx, "create_table_"+tableName, sql)
	require.NoError(db.T, err)
}

func (db *DBHelper) TruncateTable(ctx context.Context, tableName string) {
	db.T.Helper()
	sql := fmt.Sprintf(`truncate table %s;`, tableName)
	_, err := db.DB.DB().Exec(ctx, "truncate_table_"+tableName, sql)
	require.NoError(db.T, err)
}

func (db *DBHelper) DropTable(ctx context.Context, tableName string) {
	db.T.Helper()
	sql := fmt.Sprintf(`drop table if exists %s;`, tableName)
	_, err := db.DB.DB().Exec(ctx, "drop_table_"+tableName, sql)
	require.NoError(db.T, err)
}
