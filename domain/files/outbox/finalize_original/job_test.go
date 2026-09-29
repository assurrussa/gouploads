package finalizeoriginal_test

import (
	"context"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"

	finalizeoriginal "github.com/assurrussa/gouploads/domain/files/outbox/finalize_original"
)

type recordingHandler struct {
	calls int
	id    int64
	err   error
}

func (h *recordingHandler) HandleOriginal(ctx context.Context, id int64) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	h.calls++
	h.id = id
	return h.err
}

func TestJobValidatesBeforeDispatch(t *testing.T) {
	h := &recordingHandler{}
	job, err := finalizeoriginal.New(h)
	require.NoError(t, err)
	require.Equal(t, finalizeoriginal.JobName, job.Name())
	require.Error(t, job.Handle(t.Context(), `{"fileId":0}`))
	require.Zero(t, h.calls)
	require.NoError(t, job.Handle(t.Context(), `{"fileId":21}`))
	require.Equal(t, int64(21), h.id)
	sentinel := errors.New("handler failed")
	h.err = sentinel
	require.ErrorIs(t, job.Handle(t.Context(), `{"fileId":21}`), sentinel)
}

func TestJobRejectsNilHandler(t *testing.T) {
	_, err := finalizeoriginal.New(nil)
	require.Error(t, err)
	var h *recordingHandler
	_, err = finalizeoriginal.New(h)
	require.Error(t, err)
}
