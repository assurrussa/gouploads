package cleanertusuploads_test

import (
	"context"
	"testing"

	"github.com/assurrussa/goshared/pkg/tests"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	crtpkg "github.com/assurrussa/gouploads/domain/files/usecases/command/cleaner_tus_uploads"
	crtpkgmocks "github.com/assurrussa/gouploads/domain/files/usecases/command/cleaner_tus_uploads/mocks"
)

type TestUseCaseSuite struct {
	suite.Suite

	ctrl        *gomock.Controller
	mockCleaner *crtpkgmocks.Mockcleaner

	useCase *crtpkg.UseCase
}

func NewTestUseCaseSuite(t *testing.T) (context.Context, context.CancelFunc, *TestUseCaseSuite) {
	t.Helper()

	return tests.NewSuite[*TestUseCaseSuite](t, func(t *testing.T, _ context.Context) *TestUseCaseSuite {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockCleaner := crtpkgmocks.NewMockcleaner(ctrl)

		useCase := crtpkg.Must(crtpkg.NewOptions(
			mockCleaner,
		))

		return &TestUseCaseSuite{
			ctrl:        ctrl,
			mockCleaner: mockCleaner,
			useCase:     useCase,
		}
	})
}

func TestUseCase_Handle_Success(t *testing.T) {
	// Arrange.
	ctx, _, ts := NewTestUseCaseSuite(t)

	_ = ctx
	_ = ts
}

func TestUseCase_MustInit(t *testing.T) {
	_, _, ts := NewTestUseCaseSuite(t)

	ts.Panics(func() {
		crtpkg.Must(crtpkg.NewOptions(nil))
	})

	// no panics
	ts.NotPanics(func() {
		crtpkg.Must(crtpkg.NewOptions(ts.mockCleaner))
	})
}
