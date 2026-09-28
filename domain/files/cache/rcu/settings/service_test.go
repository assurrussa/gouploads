package rcusettings_test

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	rcusettings "github.com/assurrussa/gouploads/domain/files/cache/rcu/settings"
	rcusettingsmocks "github.com/assurrussa/gouploads/domain/files/cache/rcu/settings/mocks"
	tests "github.com/assurrussa/gouploads/internal/testsupport"
)

type testSuite struct {
	suite.Suite

	mockSettingsUseCase *rcusettingsmocks.MocksettingsUseCase

	svc *rcusettings.CacheService
}

func newTestSuite(t *testing.T) (context.Context, context.CancelFunc, *testSuite) {
	t.Helper()

	return tests.NewSuite[*testSuite](t, func(t *testing.T, ctx context.Context) *testSuite {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockSettingsUseCase := rcusettingsmocks.NewMocksettingsUseCase(ctrl)
		mockSettingsUseCase.EXPECT().Handle(ctx).Return(rcusettings.Data{Enabled: true}, nil).Times(1)

		svc, err := rcusettings.NewCache(
			ctx,
			logger.Discard(),
			mockSettingsUseCase,
			rcusettings.WithSyncInterval(300*time.Millisecond),
			rcusettings.WithSyncCacheInterval(120*time.Millisecond),
		)
		require.NoError(t, err)

		return &testSuite{
			svc:                 svc,
			mockSettingsUseCase: mockSettingsUseCase,
		}
	})
}

func TestConfig_ConsumersEnabled(t *testing.T) {
	ctx, _, ts := newTestSuite(t)

	assert.True(t, ts.svc.Enabled(ctx))
}

func TestConfig_ConsumersEnabledWait_Enabled(t *testing.T) {
	ctx, _, ts := newTestSuite(t)
	ts.mockSettingsUseCase.EXPECT().Handle(ctx).Return(rcusettings.Data{Enabled: true}, nil).AnyTimes()
	finished := atomic.Bool{}
	ctx, cancel := context.WithTimeout(ctx, 300*time.Millisecond)
	defer cancel()
	go func() {
		<-ctx.Done()
		finished.Store(true)
	}()

	<-ts.svc.EnabledWait(ctx)
	assert.Eventually(t, func() bool {
		return finished.Load() && ts.svc.Enabled(ctx)
	}, 3*time.Second, 10*time.Millisecond)
}

// BenchmarkConfig_ConsumersEnabled-12    	194835848	         6.141 ns/op	       0 B/op	       0 allocs/op.
func BenchmarkConfig_ConsumersEnabled(b *testing.B) {
	ctx := context.Background()
	ctrl := gomock.NewController(b)
	mockListAllRoles := rcusettingsmocks.NewMocksettingsUseCase(ctrl)
	mockListAllRoles.EXPECT().Handle(ctx).Return(rcusettings.Data{Enabled: true}, nil).AnyTimes()

	svc, err := rcusettings.NewCache(ctx, logger.Discard(), mockListAllRoles)
	require.NoError(b, err)

	b.ResetTimer()
	b.ReportAllocs()

	for i := 0; i < b.N; i++ {
		svc.Enabled(ctx)
	}
}
