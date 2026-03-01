package tusupload

import (
	"context"
	"time"

	redislib "github.com/redis/go-redis/v9"
)

//go:generate toolsmocks

type redisClient interface {
	Get(ctx context.Context, key string) *redislib.StringCmd
	Set(ctx context.Context, key string, value any, expiration time.Duration) *redislib.StatusCmd
	Del(ctx context.Context, keys ...string) *redislib.IntCmd
	ZAdd(ctx context.Context, key string, members ...redislib.Z) *redislib.IntCmd
	ZRangeByScore(ctx context.Context, key string, opt *redislib.ZRangeBy) *redislib.StringSliceCmd
	ZRem(ctx context.Context, key string, members ...any) *redislib.IntCmd
}
