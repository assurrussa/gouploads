package sendresizefile_test

import (
	"context"
	"encoding/json"
	"strconv"
	"testing"
	"time"

	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
	sendresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
)

func TestPollingContinuation(t *testing.T) {
	for _, status := range []string{"queued", "running", "done", "failed", "unknown"} {
		t.Run(status, func(t *testing.T) {
			ctx, _, ts := NewTestSuite(t)
			file := testshelpers.CreateFile(t)
			id := outboxtypes.NewJobID()
			deadline := time.Now().Add(time.Hour)
			ts.fileRepositoryMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil)
			result := clientresizer.JobResponse{JobID: id, IdempotencyKey: strconv.FormatInt(file.ID, 10), Status: status}
			if status == "done" {
				result.Artifacts = []clientresizer.JobArtifact{{Preset: "main", URL: "https://resizer.example.com/main.png"}}
			}
			ts.resizeClientMock.EXPECT().GetJob(ctx, clientresizer.JobRequest{JobID: id, TypeMedia: file.FileType.ToString()}).
				Return(result, nil)
			if status == "done" || status == "failed" {
				ts.resultMock.EXPECT().Handle(ctx, gomock.Any()).
					DoAndReturn(func(_ context.Context, r listenresizefile.Request) (listenresizefile.Response, error) {
						require.Equal(t, file.ID, r.ExternalID)
						require.Equal(t, status, r.Status)
						require.Len(t, r.Artifacts, len(result.Artifacts))
						return listenresizefile.Response{}, nil
					})
			}
			_, err := ts.useCase.Handle(ctx, sendresizefile.Request{
				FileID: file.ID, FilePath: "admitted", JobID: &id, PollDeadline: &deadline,
			})
			if status == "unknown" {
				require.ErrorContains(t, err, "reconciliation")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestPollingRejectsWrongLogicalKey(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	file := testshelpers.CreateFile(t)
	id := outboxtypes.NewJobID()
	deadline := time.Now().Add(time.Hour)
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil)
	ts.resizeClientMock.EXPECT().GetJob(ctx, gomock.Any()).Return(clientresizer.JobResponse{
		JobID: id, IdempotencyKey: "other-upload", Status: "done",
	}, nil)
	_, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FileID: file.ID, FilePath: "admitted", JobID: &id, PollDeadline: &deadline,
	})
	require.ErrorContains(t, err, "identity")
}

func TestPollingDeadlineDoesNotSubmitAgain(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	file := testshelpers.CreateFile(t)
	id := outboxtypes.NewJobID()
	deadline := time.Now().Add(-time.Minute)
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil)
	_, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FileID: file.ID, FilePath: "admitted", JobID: &id, PollDeadline: &deadline,
	})
	require.ErrorContains(t, err, "deadline")
}

func TestCompletedFileStopsPolling(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	file := testshelpers.CreateFile(t)
	file.Data.Uploader.Status = shared.FileUploadTaskStatusCompleted
	id := outboxtypes.NewJobID()
	deadline := time.Now().Add(time.Hour)
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil)
	_, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FileID: file.ID, FilePath: "admitted", JobID: &id, PollDeadline: &deadline,
	})
	require.NoError(t, err)
}

func TestAdmissionPersistsPollingIdentity(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	file := testshelpers.CreateFile(t)
	id := outboxtypes.NewJobID()
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil)
	ts.resizeClientMock.EXPECT().SendResize(ctx, gomock.Any()).Return(clientresizer.Response{JobID: id, Status: "queued"}, nil)
	ts.eventStreamMock.EXPECT().Publish(ctx, gomock.Any(), gomock.Any()).Return(nil)
	// Inspect the actually queued durable continuation.
	_, err := ts.useCase.Handle(ctx, sendresizefile.Request{FileID: file.ID, FilePath: "staging/source.png"})
	require.NoError(t, err)
	require.Len(t, *ts.queuedPolls, 1)
	encoded := (*ts.queuedPolls)[0]
	var payload shared.MediaDispatchPayload
	require.NoError(t, json.Unmarshal([]byte(encoded), &payload))
	require.Equal(t, file.ID, payload.FileID)
	require.Equal(t, id, *payload.JobID)
	require.Empty(t, payload.FilePath)
	require.NotNil(t, payload.PollDeadline)
	require.WithinDuration(t, time.Now().Add(7*24*time.Hour), *payload.PollDeadline, time.Minute)
	var restored shared.MediaDispatchPayload
	require.NoError(t, json.Unmarshal([]byte(`{"fileId":1,"filePath":"staging/source.png","skipResize":false}`), &restored))
	require.Nil(t, restored.JobID)
}

func TestPartialContinuationCannotBecomeNewSubmission(t *testing.T) {
	id := outboxtypes.NewJobID()
	deadline := time.Now().Add(time.Hour)
	for _, req := range []sendresizefile.Request{
		{FileID: 1, FilePath: "admitted", JobID: &id},
		{FileID: 1, FilePath: "admitted", PollDeadline: &deadline},
	} {
		require.ErrorContains(t, req.Validate(), "both job ID and deadline")
	}
}

func TestFailedAdmissionReplayDoesNotPublishProcessing(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	file := testshelpers.CreateFile(t)
	file.Data.Uploader.Status = shared.FileUploadTaskStatusFailed
	id := outboxtypes.NewJobID()
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil)
	ts.resizeClientMock.EXPECT().SendResize(ctx, gomock.Any()).Return(clientresizer.Response{JobID: id, Status: "queued"}, nil)
	// No event expectation: the retained admission is not a new logical attempt.
	response, err := ts.useCase.Handle(ctx, sendresizefile.Request{FileID: file.ID, FilePath: "staging/source.png"})
	require.NoError(t, err)
	require.Equal(t, id, response.JobID)
	require.Len(t, *ts.queuedPolls, 1, "replay must still reconcile the retained server outcome")
}

func TestReconciliationRequiredDoesNotBecomeTerminalFailure(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)
	file := testshelpers.CreateFile(t)
	id := outboxtypes.NewJobID()
	deadline := time.Now().Add(time.Hour)
	ts.fileRepositoryMock.EXPECT().GetByID(ctx, file.ID).Return(file, nil)
	ts.resizeClientMock.EXPECT().GetJob(ctx, gomock.Any()).Return(clientresizer.JobResponse{
		JobID: id, IdempotencyKey: strconv.FormatInt(file.ID, 10), Status: "queued",
		Admission: &clientresizer.JobAdmission{RequiresReconciliation: true, Reason: "dispatch_outcome_unknown"},
	}, nil)
	_, err := ts.useCase.Handle(ctx, sendresizefile.Request{
		FileID: file.ID, FilePath: "admitted", JobID: &id, PollDeadline: &deadline,
	})
	require.ErrorContains(t, err, "operator reconciliation")
	require.Empty(t, *ts.queuedPolls)
}
