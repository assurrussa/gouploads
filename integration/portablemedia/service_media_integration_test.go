//go:build integration

package portablemedia_test

import (
	"bytes"
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	deletedfilejob "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	sendresizefilejob "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	uploadfilejob "github.com/assurrussa/gouploads/domain/files/outbox/upload_file"
	"github.com/assurrussa/gouploads/host"
	eventstream "github.com/assurrussa/gouploads/internal/events"
)

type serviceEventCounter struct{ count atomic.Int64 }

func (e *serviceEventCounter) Publish(context.Context, host.UserID, eventstream.Event) error {
	e.count.Add(1)
	return nil
}

func testServiceLocalMedia(t *testing.T, ctx context.Context, runtime localUploadRuntime,
	uploader *host.UploadService, events *serviceEventCounter, tt scenario,
) {
	t.Helper()
	before := events.count.Load()
	principal := host.ServicePrincipal{SiteID: uuid.New(), ClientID: "cms-fixture"}
	request := host.ReaderRequest{
		Service: &principal, ObjectType: "cms_asset", ObjectID: 9001,
		FinalizationKey: uuid.NewString(),
		AfterJobs:       []host.FileEventAfterJob{{JobName: "cms_ready", Payload: `{"generation":"fixture"}`}},
	}
	input := host.ReaderUploadInput{
		OriginalName: tt.originalName, Size: int64(len(tt.sourceBody)), Reader: bytes.NewReader(tt.sourceBody),
	}
	file, err := uploader.UploadReader(ctx, request, input)
	require.NoError(t, err)
	replay, err := uploader.UploadReader(ctx, request, input)
	require.NoError(t, err)
	require.Equal(t, file.ID, replay.ID)
	require.Nil(t, file.UserID)
	require.Nil(t, file.ManagerID)
	require.Equal(t, &principal, file.GetData().Service)
	require.NoError(t, runtime.sendJob.Handle(ctx, runtime.outbox.Take(t, sendresizefilejob.JobName)))
	artifacts := make([]host.ResizeArtifactInput, 0, len(tt.artifacts))
	for _, artifact := range tt.artifacts {
		artifacts = append(artifacts, host.ResizeArtifactInput{
			Preset: artifact.preset,
			URL:    runtime.artifactServer.SignedURL(artifact.path), MediaType: artifact.mediaType,
			ContentType: artifact.contentType, Size: int64(len(artifact.body)), ExpireAt: time.Now().Add(time.Hour),
			Metadata: artifact.metadata,
		})
	}
	callback := host.BuildListenResizeRequest(host.ListenResizeInput{
		ExternalID: file.ID, Status: "done",
		Attempt: 1, Timestamp: time.Now().UTC(), Artifacts: artifacts,
	})
	_, err = runtime.listener.Handle(ctx, callback)
	require.NoError(t, err)
	payload := runtime.outbox.Take(t, uploadfilejob.JobName)
	require.NoError(t, runtime.uploadJob.Handle(ctx, payload))
	require.NoError(t, runtime.uploadJob.Handle(ctx, payload))
	after := runtime.outbox.TakeAll("cms_ready")
	require.Len(t, after, 1)
	require.JSONEq(t, `{"generation":"fixture"}`, after[0])
	completed, err := runtime.repo.GetByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, &principal, completed.GetData().Service)
	require.Empty(t, completed.GetData().Uploader)
	require.Equal(t, request.ObjectType, completed.ObjectType)
	require.Equal(t, request.ObjectID, *completed.ObjectID)
	verifyLocalFinalFiles(t, ctx, runtime, completed, tt.artifacts)
	_, err = runtime.listener.Handle(ctx, callback)
	require.NoError(t, err)
	require.Empty(t, runtime.outbox.TakeAll(uploadfilejob.JobName))
	require.Empty(t, runtime.outbox.TakeAll("cms_ready"))
	require.Equal(t, before, events.count.Load())
	require.Len(t, runtime.outbox.TakeAll(deletedfilejob.JobName), 1)
	replay, err = uploader.UploadReader(ctx, request, input)
	require.NoError(t, err)
	require.Equal(t, &principal, replay.GetData().Service)
	changed := principal
	changed.SiteID = uuid.New()
	request.Service = &changed
	_, err = uploader.UploadReader(ctx, request, input)
	require.ErrorIs(t, err, host.ErrFinalizationConflict)
}
