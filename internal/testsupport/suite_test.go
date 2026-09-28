package testsupport_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/internal/testsupport"
)

type lifecycleSuite struct {
	suite.Suite
	setup    bool
	teardown *bool
}

func (s *lifecycleSuite) SetupTest()    { s.setup = true }
func (s *lifecycleSuite) TearDownTest() { *s.teardown = true }

//nolint:tparallel // The parent verifies child cleanup after the child returns.
func TestSuiteLifecycle(t *testing.T) {
	t.Parallel()
	var ctx context.Context
	tornDown := false
	t.Run("child", func(t *testing.T) {
		var cancel context.CancelFunc
		var s *lifecycleSuite
		ctx, cancel, s = testsupport.NewSuite(t, func(t *testing.T, ctx context.Context) *lifecycleSuite {
			t.Helper()
			_ = gomock.NewController(t)
			require.NoError(t, ctx.Err())
			return &lifecycleSuite{teardown: &tornDown}
		}, testsupport.WithIsParallel(false), testsupport.WithTimeout(time.Minute))
		require.True(t, s.setup)
		require.Equal(t, t, s.T())
		cancel()
		require.ErrorIs(t, ctx.Err(), context.Canceled)
	})
	require.True(t, tornDown)
	require.ErrorIs(t, ctx.Err(), context.Canceled)
}
