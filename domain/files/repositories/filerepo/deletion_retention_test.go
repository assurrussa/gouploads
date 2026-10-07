package filerepo_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"
	"time"

	pgsqlmocks "github.com/assurrussa/outbox/backends/pgsql/storage/mocks"
	"github.com/jackc/pgx/v5"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
)

type retentionRow struct{ err error }

func (r retentionRow) Scan(dest ...any) error {
	now := time.Date(2026, 1, 3, 0, 0, 0, 0, time.UTC)
	// Partial values must be discarded if Scan fails.
	return errors.Join(
		setLifecycleValue(dest[0], now),
		setLifecycleValue(dest[1], int64(3)), setLifecycleValue(dest[2], int64(1)),
		setLifecycleValue(dest[3], int64(2)), setLifecycleValue(dest[4], int64(1)),
		setLifecycleValue(dest[5], int64(1)), setLifecycleValue(dest[6], int64(0)),
		setLifecycleValue(dest[7], int64(300)), setLifecycleValue(dest[8], int64(100)),
		setLifecycleValue(dest[9], int64(200)),
		setLifecycleValue(dest[10], sql.NullTime{Time: now.Add(-48 * time.Hour), Valid: true}),
		setLifecycleValue(dest[11], sql.NullTime{}),
		setLifecycleValue(dest[12], int64(1)), setLifecycleValue(dest[13], int64(90)), r.err,
	)
}

func TestGetDeletionRetentionReadOnlyAndErrors(t *testing.T) {
	t.Parallel()
	for _, sourceErr := range []error{nil, errors.New("scan failed")} {
		for _, withCutoff := range []bool{false, true} {
			ctrl := gomock.NewController(t)
			client := pgsqlmocks.NewMockClient(ctrl)
			db := pgsqlmocks.NewMockDBEngine(ctrl)
			tx := pgsqlmocks.NewMockTxManager(ctrl)
			var cutoff *time.Time
			var argument any
			if withCutoff {
				value := time.Date(2026, 1, 2, 0, 0, 0, 0, time.UTC)
				cutoff = &value
				argument = value
			}
			client.EXPECT().DB().Return(db)
			db.EXPECT().QueryRow(t.Context(), "filerepo.GetDeletionRetention", gomock.Any(), argument).
				DoAndReturn(func(_ context.Context, _ string, query string, _ ...any) pgx.Row {
					for _, forbidden := range []string{
						"for update", " delete ", " update ", " insert ", "upload_finalizations", " userId", "paths",
					} {
						require.NotContains(t, query, forbidden)
					}
					return retentionRow{err: sourceErr}
				})
			// No Exec, transaction or other call is allowed by these mocks.
			repo := filerepo.Must(filerepo.NewOptions(client, tx))
			result, err := repo.GetDeletionRetention(t.Context(), cutoff)
			if sourceErr != nil {
				require.ErrorIs(t, err, sourceErr)
				require.Equal(t, model.DeletionRetentionSnapshot{}, result)
				continue
			}
			require.NoError(t, err)
			require.EqualValues(t, 3, result.TotalCount)
			require.NotNil(t, result.OldestPendingCreatedAt)
			require.Nil(t, result.OldestCompletedAt)
			if withCutoff {
				require.Equal(t, &model.DeletionRetentionProjection{
					CompletedBefore: *cutoff, CompletedCount: 1, PayloadJSONTextBytesEstimate: 90,
				}, result.Projection)
			} else {
				require.Nil(t, result.Projection)
			}
		}
	}
}

func TestGetDeletionRetentionRejectsInvalidCutoffBeforeDatabase(t *testing.T) {
	t.Parallel()
	ctrl := gomock.NewController(t)
	repo := filerepo.Must(filerepo.NewOptions(pgsqlmocks.NewMockClient(ctrl), pgsqlmocks.NewMockTxManager(ctrl)))
	zero := time.Time{}
	result, err := repo.GetDeletionRetention(t.Context(), &zero)
	require.Error(t, err)
	require.Equal(t, model.DeletionRetentionSnapshot{}, result)
}
