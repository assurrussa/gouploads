//nolint:testpackage // Exercise private transaction seams without exporting test-only APIs.
package filejoboutbox

import (
	"context"
	"errors"
	"testing"
	"time"

	pgsql "github.com/assurrussa/outbox/backends/pgsql/storage"
	core "github.com/assurrussa/outbox/outbox"
	"github.com/assurrussa/outbox/shared/types"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgconn"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
)

type fakeTx struct {
	pgx.Tx
	err     error
	linkErr error
	links   int
}
type fakeRow struct{ err error }

func (r fakeRow) Scan(dest ...any) error {
	if r.err != nil {
		return r.err
	}
	value, ok := dest[0].(*bool)
	if !ok {
		return errors.New("unexpected fixture scan destination")
	}
	*value = true
	return nil
}
func (t *fakeTx) QueryRow(context.Context, string, ...any) pgx.Row { return fakeRow{t.err} }
func (t *fakeTx) Exec(context.Context, string, ...any) (pgconn.CommandTag, error) {
	t.links++
	return pgconn.NewCommandTag("INSERT 0 1"), t.linkErr
}

type fakeScope struct {
	tx        *fakeTx
	finalErr  error
	noContext bool
}

func (s fakeScope) RunInTx(ctx context.Context, fn func(context.Context) error) error {
	if s.noContext {
		return fn(ctx)
	}
	if err := fn(pgsql.WithTx(ctx, s.tx)); err != nil {
		return err
	}
	return s.finalErr
}

type fakeJobs struct {
	t     *testing.T
	tx    *fakeTx
	id    types.JobID
	err   error
	calls int
}

func (j *fakeJobs) CreateJobVersioned(
	ctx context.Context, _ string, version core.SchemaVersion, _ string, _ time.Time,
) (types.JobID, error) {
	require.Same(j.t, j.tx, pgsql.GetTx(ctx))
	require.Equal(j.t, core.DefaultSchemaVersion, version)
	j.calls++
	return j.id, j.err
}

func TestAssociatedPutPreservesTransactionAndUncertainty(t *testing.T) {
	fail := errors.New("synthetic failure")
	for _, tc := range []struct {
		name                               string
		readErr, putErr, linkErr, finalErr error
		noContext                          bool
		wantPuts, wantLinks                int
	}{
		{name: "same transaction", wantPuts: 1, wantLinks: 1},
		{name: "binding changed", readErr: pgx.ErrNoRows},
		{name: "put failed", putErr: fail, wantPuts: 1},
		{name: "link failed", linkErr: fail, wantPuts: 1, wantLinks: 1},
		{name: "ambiguous commit", finalErr: context.DeadlineExceeded, wantPuts: 1, wantLinks: 1},
		{name: "canceled commit", finalErr: context.Canceled, wantPuts: 1, wantLinks: 1},
		{name: "transaction unavailable", noContext: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tx := &fakeTx{err: tc.readErr, linkErr: tc.linkErr}
			jobs := &fakeJobs{t: t, tx: tx, id: types.NewJobID(), err: tc.putErr}
			queue := &Outbox{jobs: jobs, tx: fakeScope{tx: tx, finalErr: tc.finalErr, noContext: tc.noContext}}
			id, err := queue.PutFileJob(t.Context(), model.File{ID: 9, Slug: "owned-generation"},
				model.FileJobOriginalFinalization, "finalize_original_file", "synthetic", time.Now())
			if tc.name == "same transaction" {
				require.NoError(t, err)
				require.Equal(t, jobs.id, id)
			} else {
				require.Error(t, err)
				require.Equal(t, types.JobIDNil, id)
			}
			require.Equal(t, tc.wantPuts, jobs.calls)
			require.Equal(t, tc.wantLinks, tx.links)
		})
	}
}

func TestAssociatedPutRejectsInvalidBindingBeforeQueue(t *testing.T) {
	queue := &Outbox{}
	for _, file := range []model.File{{}, {ID: 9}, {Slug: "generation"}} {
		_, err := queue.PutFileJob(t.Context(), file, model.FileJobDeletion, "deleted_file", "{}", time.Now())
		require.Error(t, err)
	}
	_, err := queue.PutFileJob(t.Context(), model.File{ID: 9, Slug: "generation"}, "invalid", "name", "{}", time.Now())
	require.Error(t, err)
	_, err = New(nil)
	require.Error(t, err)
}
