package filerepo_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	pgsqlmocks "github.com/assurrussa/outbox/backends/pgsql/storage/mocks"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	"github.com/assurrussa/gouploads/domain/files/shared"
)

type lifecycleRow struct {
	err error
}

func (r lifecycleRow) Scan(dest ...any) error {
	// Simulate a driver partially filling the result before a decoding error.
	return errors.Join(
		setLifecycleValue(dest[0], true),
		setLifecycleValue(dest[1], true),
		setLifecycleValue(dest[2], shared.FileUploadTaskStatusFailed),
		setLifecycleValue(dest[3], false),
		setLifecycleValue(dest[4], true),
		setLifecycleValue(dest[5], true),
		setLifecycleValue(dest[6], false),
		r.err,
	)
}

func setLifecycleValue[T any](target any, value T) error {
	pointer, ok := target.(*T)
	if !ok {
		return errors.New("unexpected lifecycle scan destination")
	}
	*pointer = value
	return nil
}

func TestGetFileLifecycleReadOnlyAndErrors(t *testing.T) {
	t.Parallel()
	for _, sourceErr := range []error{nil, errors.New("driver failure")} {
		t.Run(map[bool]string{true: "error", false: "success"}[sourceErr != nil], func(t *testing.T) {
			t.Parallel()
			ctrl := gomock.NewController(t)
			client := pgsqlmocks.NewMockClient(ctrl)
			db := pgsqlmocks.NewMockDBEngine(ctrl)
			tx := pgsqlmocks.NewMockTxManager(ctrl)
			client.EXPECT().DB().Return(db)
			db.EXPECT().QueryRow(t.Context(), "filerepo.GetFileLifecycle", gomock.Any(), int64(42)).
				DoAndReturn(func(_ context.Context, _ string, query string, _ ...any) pgx.Row {
					require.True(t, strings.HasPrefix(query, "select"))
					for _, forbidden := range []string{"for update", "payload", "binding_hash", "finalization_key"} {
						require.NotContains(t, query, forbidden)
					}
					return lifecycleRow{err: sourceErr}
				})
			repo := filerepo.Must(filerepo.NewOptions(client, tx))
			result, err := repo.GetFileLifecycle(t.Context(), 42)
			if sourceErr != nil {
				require.ErrorIs(t, err, sourceErr)
				require.Equal(t, model.FileLifecycleSnapshot{}, result)
				return
			}
			require.NoError(t, err)
			require.Equal(t, model.FileLifecycleSnapshot{
				FileExists: true, FileDeleted: true, UploadStatus: shared.FileUploadTaskStatusFailed,
				FinalizationRecorded: true, DeletionPlanned: true,
			}, result)
		})
	}
}

func TestGetFileLifecycleRejectsInvalidIDBeforeDatabase(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	repo := filerepo.Must(filerepo.NewOptions(pgsqlmocks.NewMockClient(ctrl), pgsqlmocks.NewMockTxManager(ctrl)))
	for _, id := range []int64{0, -1} {
		result, err := repo.GetFileLifecycle(t.Context(), id)
		require.Error(t, err)
		require.Equal(t, model.FileLifecycleSnapshot{}, result)
	}
}
