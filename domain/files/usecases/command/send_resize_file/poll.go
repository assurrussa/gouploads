package sendresizefile

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/assurrussa/gouploads/domain/files/model"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
	"github.com/assurrussa/gouploads/internal/filejobs"
)

const pollInterval = 30 * time.Second

func (u *UseCase) schedulePoll(ctx context.Context, file model.File, req Request) error {
	payload, err := json.Marshal(shared.MediaDispatchPayload{
		FileID: req.FileID, FilePath: req.FilePath, SkipResizeVideo: req.SkipResizeVideo,
		JobID: req.JobID, PollDeadline: req.PollDeadline,
	})
	if err != nil {
		return fmt.Errorf("marshal media polling continuation: %w", err)
	}
	if _, err := filejobs.Put(
		ctx, u.outbox, file, model.FileJobMediaAdmission, "send_resize_file",
		string(payload), time.Now().Add(pollInterval),
	); err != nil {
		return fmt.Errorf("persist media polling continuation: %w", err)
	}
	return nil
}

func (u *UseCase) poll(ctx context.Context, file model.File, req Request) (Response, error) {
	if req.JobID.IsZero() || req.PollDeadline == nil || !req.PollDeadline.After(time.Now()) {
		return Response{}, errors.New("media polling deadline reached or invalid continuation; operator reconciliation required")
	}
	result, err := u.remoteClient.GetJob(ctx, clientresizer.JobRequest{JobID: *req.JobID, TypeMedia: file.FileType.ToString()})
	if err != nil {
		return Response{}, fmt.Errorf("poll admitted media job: %w", err)
	}
	if result.JobID != *req.JobID || result.IdempotencyKey != strconv.FormatInt(file.ID, 10) {
		return Response{}, errors.New("media job does not match persisted upload identity")
	}
	response := Response{JobID: result.JobID, Status: result.Status}
	if result.Status == "unknown" || (result.Admission != nil && result.Admission.RequiresReconciliation) {
		return Response{}, errors.New("media processing outcome unknown; operator reconciliation required")
	}
	switch result.Status {
	case "queued", "running":
		return response, u.schedulePoll(ctx, file, req)
	case "done", "failed":
		artifacts := make([]listenresizefile.Artifact, 0, len(result.Artifacts))
		for _, a := range result.Artifacts {
			artifacts = append(artifacts, listenresizefile.Artifact{
				Preset: a.Preset, URL: a.URL, MediaType: a.MediaType,
				ContentType: a.ContentType, Size: a.Size, ExpireAt: a.ExpireAt, Metadata: a.Metadata,
			})
		}
		_, err := u.resultHandler.Handle(ctx, listenresizefile.Request{
			ExternalID: file.ID, Status: result.Status, Artifacts: artifacts,
		})
		return response, err
	default:
		return Response{}, errors.New("invalid admitted media job status")
	}
}
