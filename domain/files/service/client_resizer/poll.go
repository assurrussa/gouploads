package clientresizer

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	outboxtypes "github.com/assurrussa/outbox/shared/types"
)

type JobRequest struct {
	JobID     outboxtypes.JobID
	TypeMedia string
}

type JobArtifact struct {
	Preset      string         `json:"preset"`
	URL         string         `json:"url"`
	MediaType   string         `json:"media_type"`   //nolint:tagliatelle // external contract
	ContentType string         `json:"content_type"` //nolint:tagliatelle // external contract
	Size        int64          `json:"size"`
	ExpireAt    time.Time      `json:"expire_at"` //nolint:tagliatelle // external contract
	Metadata    map[string]any `json:"metadata"`
}

type JobResponse struct {
	JobID          outboxtypes.JobID `json:"job_id"`          //nolint:tagliatelle // external contract
	IdempotencyKey string            `json:"idempotency_key"` //nolint:tagliatelle // external contract
	Status         string            `json:"status"`
	Artifacts      []JobArtifact     `json:"artifacts"`
	Admission      *JobAdmission     `json:"admission,omitempty"`
}

type JobAdmission struct {
	RequiresReconciliation bool   `json:"requires_reconciliation"` //nolint:tagliatelle // external contract
	Reason                 string `json:"reason"`
}

// GetJob uses the same configured origin and token scope as submission. It does
// not follow a server-provided URL or expose processor errors/signed artifact URLs
// in errors. The caller verifies the idempotency key against its persisted file.
func (s *Service) GetJob(ctx context.Context, req JobRequest) (JobResponse, error) {
	if req.JobID.IsZero() || (req.TypeMedia != "image" && req.TypeMedia != "video") {
		return JobResponse{}, errors.New("invalid media job lookup")
	}
	endpoint, err := url.Parse(s.mediaResizerURL(req.TypeMedia))
	if err != nil {
		return JobResponse{}, errors.New("invalid media job endpoint")
	}
	endpoint.Path = strings.TrimRight(endpoint.Path, "/") + "/" + req.JobID.String()
	endpoint.RawPath = ""
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint.String(), nil)
	if err != nil {
		return JobResponse{}, errors.New("create media job lookup")
	}
	if token := s.tokenForType(ctx, req.TypeMedia); token != "" {
		request.Header.Set("Authorization", "Bearer "+token)
		request.Header.Set("X-API-Token", token)
	}
	response, err := s.client.Do(request)
	if err != nil {
		return JobResponse{}, fmt.Errorf("get media job: %w", redactTransportURL(err))
	}
	if response == nil || response.Body == nil {
		return JobResponse{}, errors.New("get media job: empty response")
	}
	defer func() { _ = response.Body.Close() }()
	if response.StatusCode != http.StatusOK {
		return JobResponse{}, &AdmissionError{
			StatusCode: response.StatusCode,
			RetryAfter: retryAfter(response.Header.Get("Retry-After"), time.Now()),
		}
	}
	body, err := io.ReadAll(io.LimitReader(response.Body, 1024*1024+1))
	if err != nil {
		return JobResponse{}, fmt.Errorf("read media job: %w", err)
	}
	if len(body) > 1024*1024 {
		return JobResponse{}, errors.New("media job response exceeds 1 MiB")
	}
	var result JobResponse
	if err := json.Unmarshal(body, &result); err != nil {
		return JobResponse{}, errors.New("decode media job response")
	}
	if result.JobID != req.JobID || result.IdempotencyKey == "" {
		return JobResponse{}, errors.New("media job identity mismatch")
	}
	switch result.Status {
	case "queued", "running", "done", "failed", "unknown":
	default:
		return JobResponse{}, errors.New("invalid media job status")
	}
	return result, nil
}
