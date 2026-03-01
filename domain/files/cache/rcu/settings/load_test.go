package rcusettings_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	rcucachesettings "github.com/assurrussa/gouploads/domain/files/cache/rcu/settings"
)

func TestLoadDataResponse(t *testing.T) {
	t.Parallel()

	useCase := &fakeRolesAllUseCase{
		err: errors.New("error"),
	}
	//nolint:nolintlint,ineffassign // it's valid
	data, err := rcucachesettings.LoadData(useCase)(context.Background())
	require.ErrorIs(t, err, useCase.err)
	require.Nil(t, data)

	useCase = &fakeRolesAllUseCase{
		resp: rcucachesettings.Data{Enabled: true},
	}

	//nolint:nolintlint,ineffassign // it's valid
	data, err = rcucachesettings.LoadData(useCase)(context.Background())
	require.NoError(t, err)

	dataAll, ok := data["all"]
	require.True(t, ok)

	assert.True(t, dataAll.Enabled)
}

type fakeRolesAllUseCase struct {
	resp rcucachesettings.Data
	err  error
}

func (f *fakeRolesAllUseCase) Handle(context.Context) (rcucachesettings.Data, error) {
	return f.resp, f.err
}
