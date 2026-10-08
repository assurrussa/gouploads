package sendresizefile

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
)

type associatedPollQueue struct {
	t         *testing.T
	file      model.File
	operation model.FileJobOperation
	payload   string
	queueID   types.JobID
}

func (q *associatedPollQueue) Put(context.Context, string, string, time.Time) (types.JobID, error) {
	q.t.Fatal("continuation must use explicit association")
	return types.JobIDNil, nil
}
func (q *associatedPollQueue) PutFileJob(_ context.Context, file model.File, operation model.FileJobOperation,
	name, payload string, _ time.Time,
) (types.JobID, error) {
	require.Equal(q.t, "send_resize_file", name)
	q.file, q.operation, q.payload = file, operation, payload
	return q.queueID, nil
}

func TestPollAssociationKeepsRemoteAndQueueIdentitiesSeparate(t *testing.T) {
	remoteID, queueID := types.NewJobID(), types.NewJobID()
	deadline := time.Now().Add(time.Hour)
	file := model.File{ID: 23, Slug: "owned-generation"}
	queue := &associatedPollQueue{t: t, queueID: queueID}
	usecase := &UseCase{Options: Options{outbox: queue}}
	err := usecase.schedulePoll(t.Context(), file, Request{
		FileID: file.ID, JobID: &remoteID, PollDeadline: &deadline,
	})
	require.NoError(t, err)
	require.Equal(t, file, queue.file)
	require.Equal(t, model.FileJobMediaAdmission, queue.operation)
	var payload shared.MediaDispatchPayload
	require.NoError(t, json.Unmarshal([]byte(queue.payload), &payload))
	require.Equal(t, remoteID, *payload.JobID)
	require.NotEqual(t, queueID, *payload.JobID)
}
