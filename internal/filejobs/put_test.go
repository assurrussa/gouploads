package filejobs_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/internal/filejobs"
)

type plainQueue struct{ calls int }

func (q *plainQueue) Put(context.Context, string, string, time.Time) (types.JobID, error) {
	q.calls++
	return types.NewJobID(), nil
}

type linkedQueue struct {
	plainQueue
	calls     int
	file      model.File
	operation model.FileJobOperation
	err       error
}

func (q *linkedQueue) PutFileJob(
	_ context.Context, file model.File, op model.FileJobOperation, _, _ string, _ time.Time,
) (types.JobID, error) {
	q.calls++
	q.file, q.operation = file, op
	return types.NewJobID(), q.err
}

func TestPutPreservesUnmappedQueuesAndNeverInfersStagingIdentity(t *testing.T) {
	plain := &plainQueue{}
	file := model.File{ID: 11, Slug: "owned"}
	_, err := filejobs.Put(t.Context(), plain, file, model.FileJobMediaAdmission, "send_resize_file", "not JSON", time.Now())
	require.NoError(t, err)
	require.Equal(t, 1, plain.calls)
	for _, legacy := range []model.File{{}, {ID: 11}, {Slug: "staging"}} {
		q := &linkedQueue{}
		_, err = filejobs.Put(t.Context(), q, legacy, model.FileJobDeletion, "deleted_file", "not JSON", time.Now())
		require.NoError(t, err)
		require.Zero(t, q.calls)
		require.Equal(t, 1, q.plainQueue.calls)
	}
}

func TestPutForwardsExplicitProvenanceAndDoesNotFallBackAfterFailure(t *testing.T) {
	fail := errors.New("uncertain commit")
	q := &linkedQueue{err: fail}
	file := model.File{ID: 11, Slug: "owned"}
	_, err := filejobs.Put(t.Context(), q, file, model.FileJobMediaFinalization, "upload_file", "not JSON", time.Now())
	require.ErrorIs(t, err, fail)
	require.Equal(t, file, q.file)
	require.Equal(t, model.FileJobMediaFinalization, q.operation)
	require.Equal(t, 1, q.calls)
	require.Zero(t, q.plainQueue.calls)
}
