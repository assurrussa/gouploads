//go:build integration

package testshelpers

import (
	"context"
	"testing"
	"time"

	"github.com/assurrussa/goredis/redisinit"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

type OptionRedis func(cfg redis.Config) redis.Config

func WithRedisDatabaseNumber(number int) OptionRedis {
	return func(cfg redis.Config) redis.Config {
		cfg.Database = number

		return cfg
	}
}

func WithRedisAddress(address ...string) OptionRedis {
	return func(cfg redis.Config) redis.Config {
		cfg.Addrs = address

		return cfg
	}
}

func PrepareRedis(
	ctx context.Context,
	t *testing.T,
	dbName string,
	opts ...OptionRedis,
) (redisPool redis.ClientContract, cleanUp func(ctx context.Context)) {
	t.Helper()
	require.NotEmpty(t, dbName)

	lg := CreateLogger(t).WithNamed(dbName)

	cfg := redis.Config{
		Addrs:          []string{},
		ClientName:     dbName,
		ConnTimeout:    time.Second,
		ReadTimeout:    time.Second,
		WriteTimeout:   time.Second,
		Database:       0,
		PoolSize:       3,
		Password:       "",
		Username:       "",
		TLSCert:        "",
		TLSKey:         "",
		RouteByLatency: false,
		RouteRandomly:  false,
		Check:          true,
	}

	for _, opt := range opts {
		cfg = opt(cfg)
	}

	redisPool, err := redisinit.NewPoolShards(ctx, cfg)
	require.NoError(t, err)

	lg.InfoContext(ctx, "redisPool created for tests")

	return redisPool, func(ctx context.Context) {
		defer lg.InfoContext(ctx, "redisPool finished for tests")
		assert.NoError(t, redisPool.Close())
	}
}
