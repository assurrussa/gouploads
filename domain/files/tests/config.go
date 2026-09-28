//go:build integration

package testshelpers

import (
	"context"
	"fmt"
	"log/slog"
	"path/filepath"
	"time"

	"github.com/ilyakaznacheev/cleanenv"
	"github.com/joho/godotenv"

	"github.com/assurrussa/gouploads/internal/testsupport"
	validator "github.com/assurrussa/gouploads/internal/validation"
)

type CleanUp func(ctx context.Context)

var Config configIntegration

type configIntegration struct {
	Env         string `env:"TEST_APP_ENV" env-default:"stage" validate:"required,oneof=local dev stage prod"`
	AppName     string `env:"TEST_APP_NAME" env-default:"test-tgmulti-service" validate:"required"`
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
	TLSCert              string        `env:"TEST_PSQL_TLS_CERT"`
	TLSKey               string        `env:"TEST_PSQL_TLS_KEY"`
	MaxConnIdleTime      time.Duration `env:"TEST_PSQL_MAX_CONN_IDLE_TIME" env-default:"5m" validate:"min=1s,max=1h"`
	MaxConnLifeTime      time.Duration `env:"TEST_PSQL_MAX_CONN_LIFE_TIME" env-default:"1h" validate:"min=1m"`
}

func init() {
	testsupport.LoadEnvironment(testsupport.CallerCurrentFile())

	callerFile := testsupport.CallerCurrentFile()
	filePath := testsupport.FindFileDir(".env", callerFile)
	if err := godotenv.Load(filePath); err != nil {
		slog.Default().Warn("not found .env file")
	}

	err := cleanenv.ReadEnv(&Config)
	if err != nil {
		panic(fmt.Errorf("read error testing env: %v", err))
	}

	err = validator.Validator.Struct(Config)
	if err != nil {
		panic(fmt.Sprintf("validate testing config: %v", err))
	}

	Config.CurrentPath = filepath.Dir(filePath)
	Config.BasePath, err = testsupport.FindBasePath()
	if err != nil {
		panic(fmt.Sprintf("find base path: %v", err))
	}
}
