//go:build integration

package hosttest

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/goshared/pkg/filecaller"
	"github.com/assurrussa/goshared/pkg/loadenv"
	"github.com/assurrussa/goshared/pkg/tests/utilst"
	"github.com/assurrussa/goshared/pkg/validator"
	"github.com/assurrussa/outbox/backends/pgsql/migrator"
	pgsqlpgx "github.com/assurrussa/outbox/backends/pgsql/storage"
	pgsqlclient "github.com/assurrussa/outbox/backends/pgsql/storage/pgsqlclient"
	"github.com/assurrussa/outbox/backends/pgsql/storage/pgsqlinit"
	"github.com/google/uuid"
	"github.com/ilyakaznacheev/cleanenv"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/joho/godotenv"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type CleanUp func(ctx context.Context)

type PgsqlClient = pgsqlclient.Client

type OptionDatabase func(*OptionsDatabase)

type OptionsDatabase struct {
	pathFilesMigration []string
	fixedDBName        bool
	verbose            bool
	log                func(string, ...any)
}

type DBHelper struct {
	T  *testing.T
	DB *pgsqlclient.Client
}

type integrationConfig struct {
	Env         string `env:"TEST_APP_ENV" env-default:"stage" validate:"required,oneof=local dev stage prod"`
	AppName     string `env:"TEST_APP_NAME" env-default:"test-gouploads-consumer" validate:"required"`
	AppVersion  string `env:"TEST_APP_VERSION" env-default:"integration-v1.0.0"`
	LogLevel    string `env:"TEST_LOG_LEVEL" env-default:"info" validate:"required,oneof=debug info warn error"`
	LogJSON     bool   `env:"TEST_LOG_JSON" env-default:"true" validate:"boolean"`
	LogOutput   string `env:"TEST_LOG_OUTPUT" env-default:""`
	CurrentPath string `env:"TEST_CURRENT_PATH" env-default:""`
	BasePath    string `env:"BASE_PATH" env-default:""`

	PostgresAddress      string        `env:"TEST_PSQL_ADDRESS" env-default:"integration-postgres-tests" validate:"required"`
	PostgresPort         int           `env:"TEST_PSQL_PORT" env-default:"5432" validate:"required"`
	PostgresAddressLocal string        `env:"TEST_PSQL_ADDRESS_LOCAL"`
	PostgresPortLocal    int           `env:"TEST_PSQL_PORT_LOCAL"`
	PostgresUser         string        `env:"TEST_PSQL_USERNAME" env-default:"tests-service" validate:"required"`
	PostgresPassword     string        `env:"TEST_PSQL_PASSWORD" env-default:"tests-service" validate:"required"`
	PostgresDatabase     string        `env:"TEST_PSQL_DATABASENAME" env-default:"tests-db-pgsql" validate:"required"`
	PostgresSSLMode      string        `env:"TEST_PSQL_SSL_MODE" env-default:"disable" validate:"required"`
	PostgresDebug        bool          `env:"TEST_PSQL_DEBUG" env-default:"false"`
	MinConnectionsCount  int32         `env:"TEST_PSQL_MIN_CONN_COUNT" env-default:"1" validate:"min=1"`
	MaxConnectionsCount  int32         `env:"TEST_PSQL_MAX_CONN_COUNT" env-default:"100" validate:"min=1"`
	MaxConnIdleTime      time.Duration `env:"TEST_PSQL_MAX_CONN_IDLE_TIME" env-default:"5m" validate:"min=1s,max=1h"`
	MaxConnLifeTime      time.Duration `env:"TEST_PSQL_MAX_CONN_LIFE_TIME" env-default:"1h" validate:"min=1m"`
}

var (
	configOnce     sync.Once
	configValue    integrationConfig
	configErr      error
	migrationLock  sync.Mutex
	runRateLimitCh = make(chan struct{}, 3)
)

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
	ctx context.Context,
	t *testing.T,
	dbName string,
	opts ...OptionDatabase,
) (*PgsqlClient, *DBHelper, CleanUp) {
	t.Helper()
	require.NotEmpty(t, dbName)

	cfg := readIntegrationConfig(t)
	select {
	case <-ctx.Done():
		t.Logf("Warning: context done: %v", ctx.Err())
		return nil, nil, nil
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

	lg := createLogger(t).WithNamed(dbName)
	psqlCfg := pgsqlpgx.PSQLConfig{
		Address:             postgresAddress(cfg),
		Username:            cfg.PostgresUser,
		Password:            cfg.PostgresPassword,
		Database:            cfg.PostgresDatabase,
		SSLMode:             cfg.PostgresSSLMode,
		DebugMode:           cfg.PostgresDebug,
		MinConnectionsCount: cfg.MinConnectionsCount,
		MaxConnectionsCount: cfg.MaxConnectionsCount,
		MaxConnIdleTime:     cfg.MaxConnIdleTime,
		MaxConnLifeTime:     cfg.MaxConnLifeTime,
	}
	poolMain, err := pgsqlinit.CreateWithConfig(ctx, psqlCfg, pgsqlclient.WithEnvironment(cfg.Env), pgsqlclient.WithLogger(lg))
	require.NoError(t, err)
	require.NoError(t, createDatabase(ctx, dbName, poolMain))

	psqlCfg.Database = dbName
	pool, err := pgsqlinit.CreateWithConfig(ctx, psqlCfg, pgsqlclient.WithEnvironment(cfg.Env), pgsqlclient.WithLogger(lg))
	require.NoError(t, err)
	migrateDatabase(t, ctx, cfg, pool, "up", options, lg)

	db := &DBHelper{T: t, DB: pool}

	return pool, db, func(ctx context.Context) {
		migrateDatabase(t, ctx, cfg, pool, "reset", options, lg)

		assert.NoError(t, pool.Close())
		assert.NoError(t, dropDatabaseIfExists(ctx, dbName, poolMain))
		assert.NoError(t, poolMain.Close())
	}
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

func readIntegrationConfig(t *testing.T) integrationConfig {
	t.Helper()

	configOnce.Do(func() {
		configValue, configErr = loadIntegrationConfig()
	})
	require.NoError(t, configErr)

	return configValue
}

func loadIntegrationConfig() (integrationConfig, error) {
	loadenv.Load()

	callerFile := filecaller.CallerCurrentFile()
	filePath := filecaller.FindFileDir(".env", callerFile)
	if err := godotenv.Load(filePath); err != nil {
		slog.Default().Warn("not found .env file")
	}

	var cfg integrationConfig
	if err := cleanenv.ReadEnv(&cfg); err != nil {
		return integrationConfig{}, fmt.Errorf("read error testing env: %w", err)
	}
	if err := validator.Validator.Struct(cfg); err != nil {
		return integrationConfig{}, fmt.Errorf("validate testing config: %w", err)
	}

	cfg.CurrentPath = filepath.Dir(filePath)
	basePath, err := utilst.FindBasePath()
	if err != nil {
		return integrationConfig{}, fmt.Errorf("find base path: %w", err)
	}
	cfg.BasePath = basePath

	return cfg, nil
}

func postgresAddress(cfg integrationConfig) string {
	port := cfg.PostgresPort
	if cfg.PostgresPortLocal > 0 {
		port = cfg.PostgresPortLocal
	}
	address := cfg.PostgresAddress
	if cfg.PostgresAddressLocal != "" {
		address = cfg.PostgresAddressLocal
	}

	return address + ":" + strconv.Itoa(port)
}

func createLogger(t *testing.T) *logger.Log {
	t.Helper()

	log, err := logger.NewLogger(logger.Config{
		Env:       "production",
		Level:     "info",
		JSON:      true,
		Rate:      0.0,
		AddSource: false,
	})
	require.NoError(t, err)

	return log
}

func migrateDatabase(
	t *testing.T,
	ctx context.Context,
	cfg integrationConfig,
	pool *pgsqlclient.Client,
	command string,
	options *OptionsDatabase,
	lg logger.Logger,
) {
	t.Helper()
	migrationLock.Lock()
	defer migrationLock.Unlock()

	pgxPool, okPgxPool := pool.DB().(pgsqlpgx.DBPgxEnginePool)
	if !okPgxPool {
		t.Fatalf("pgsqlpgx.DBPgxEnginePool is not DB pool")
	}
	sqlDB := stdlib.OpenDBFromPool(pgxPool.Pool())
	defer func() { assert.NoError(t, sqlDB.Close()) }()

	for _, path := range options.pathFilesMigration {
		dir := strings.Replace(path, cfg.BasePath, "", 1)
		dir = filepath.Join(cfg.BasePath, dir)

		err := migrator.Run(
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
		return fmt.Errorf("drop db %s: %w", dbName, err)
	}

	if _, err := pool.DB().Exec(ctx, "createDatabase", fmt.Sprintf("CREATE DATABASE %q", dbName)); err != nil {
		return fmt.Errorf("create db %s: %w", dbName, err)
	}

	return nil
}

func dropDatabaseIfExists(ctx context.Context, dbName string, pool *pgsqlclient.Client) error {
	_, err := pool.DB().Exec(ctx, "dropDatabase", fmt.Sprintf("DROP DATABASE IF EXISTS %q", dbName))
	return err
}
