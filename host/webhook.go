package host

import (
	"context"
	"time"

	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
)

type (
	ListenResizeCommand  = listenresizefile.UseCase
	ListenResizeRequest  = listenresizefile.Request
	ListenResizeResponse = listenresizefile.Response
	ResizeArtifact       = listenresizefile.Artifact
)

type ListenResizeHandler interface {
	Handle(ctx context.Context, req ListenResizeRequest) (ListenResizeResponse, error)
}

type ResizeArtifactInput struct {
	Preset      string
	URL         string
	MediaType   string
	ContentType string
	Size        int64
	ExpireAt    time.Time
	Metadata    map[string]any
}

type ListenResizeInput struct {
	ExternalID int64
	Status     string
	Attempt    int
	Error      string
	Metadata   map[string]any
	Timestamp  time.Time
	Artifacts  []ResizeArtifactInput
}

func BuildListenResizeRequest(input ListenResizeInput) ListenResizeRequest {
	attempt := input.Attempt
	if attempt < 0 {
		attempt = 0
	}

	hasArtifact := make(map[string]struct{}, len(input.Artifacts))
	artifacts := make([]ResizeArtifact, 0, len(input.Artifacts))
	for _, artifact := range input.Artifacts {
		if _, found := hasArtifact[artifact.Preset]; found {
			continue
		}
		hasArtifact[artifact.Preset] = struct{}{}
		artifacts = append(artifacts, ResizeArtifact{
			Preset:      artifact.Preset,
			URL:         artifact.URL,
			MediaType:   artifact.MediaType,
			ContentType: artifact.ContentType,
			Size:        artifact.Size,
			ExpireAt:    artifact.ExpireAt,
			Metadata:    artifact.Metadata,
		})
	}

	return ListenResizeRequest{
		ExternalID: input.ExternalID,
		Status:     input.Status,
		Attempt:    uint32(attempt),
		Error:      input.Error,
		Metadata:   input.Metadata,
		Timestamp:  input.Timestamp,
		Artifacts:  artifacts,
	}
}
