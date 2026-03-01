package rcusettings

import (
	"context"
	"fmt"
	"time"

	rcu2 "github.com/assurrussa/goshared/pkg/cache/rcu"
	"github.com/assurrussa/goshared/pkg/logger"
)

//go:generate toolsmocks

const (
	cacheKeyAll = "all"
)

type settingsUseCase interface {
	Handle(ctx context.Context) (Data, error)
}

type Option func(*CacheService)

func WithSyncInterval(duration time.Duration) Option {
	return func(c *CacheService) {
		c.syncInterval = duration
	}
}

func WithSyncCacheInterval(duration time.Duration) Option {
	return func(c *CacheService) {
		c.syncCacheInterval = duration
	}
}

type Data struct {
	Enabled           bool
	ImageResizerToken string
	VideoResizerToken string
}

type CacheService struct {
	cache             *rcu2.Cache[string, Data]
	syncInterval      time.Duration
	syncCacheInterval time.Duration
}

func NewCache(ctx context.Context, log logger.Logger, rolesAllUseCase settingsUseCase, opts ...Option) (*CacheService, error) {
	c := &CacheService{
		syncInterval:      5 * time.Second,
		syncCacheInterval: 4 * time.Second,
	}

	for _, opt := range opts {
		opt(c)
	}

	c.cache = rcu2.NewCache[string, Data](
		ctx, log, LoadData(rolesAllUseCase), rcu2.WithSyncInterval[string, Data](c.syncCacheInterval),
	)
	if err := <-c.cache.WaitLoading(); err != nil {
		return nil, fmt.Errorf("configurator: failed to load configs: %w", err)
	}

	return c, nil
}

func (c *CacheService) Enabled(_ context.Context) bool {
	res, _ := c.cache.Get(cacheKeyAll)
	return res.Enabled
}

func (c *CacheService) EnabledWait(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{})
	go func() {
		defer close(ch)

		for {
			if c.Enabled(ctx) {
				return
			}

			select {
			case <-ctx.Done():
				return
			case <-time.After(c.syncInterval):
				continue
			}
		}
	}()

	return ch
}

func (c *CacheService) Tokens(_ context.Context) (imageToken string, videoToken string) {
	res, _ := c.cache.Get(cacheKeyAll)
	return res.ImageResizerToken, res.VideoResizerToken
}
