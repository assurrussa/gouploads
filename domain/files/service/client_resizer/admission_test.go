package clientresizer_test

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
)

func TestAdmissionResponses(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		code       int
		wantError  bool
	}{
		{"accepted", `{"job_id":"9d435a36-6fad-4a52-a0a7-d474d3393ab3","status":"queued"}`, 202, false},
		{"empty", `{}`, 202, true},
		{"null", `null`, 202, true},
		{"zero ID", `{"job_id":"00000000-0000-0000-0000-000000000000","status":"queued"}`, 202, true},
		{"invalid state", `{"job_id":"9d435a36-6fad-4a52-a0a7-d474d3393ab3","status":"unexpected"}`, 202, true},
		{"oversize", strings.Repeat(" ", 64*1024+1), 202, true},
		{"conflict", `{"error":"secret source signature"}`, 409, true},
		{"capacity", `{"error":"secret source signature"}`, 503, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				w.Header().Set("Retry-After", "7")
				w.WriteHeader(tc.code)
				_, _ = io.WriteString(w, tc.body)
			}))
			defer server.Close()
			client := clientresizer.Must(clientresizer.NewOptions(
				server.Client(), server.URL+"/jobs", server.URL+"/jobs", logger.Discard(),
			))
			_, err := client.SendResize(context.Background(), clientresizer.Request{TypeMedia: "image", Data: []byte(`{}`)})
			if !tc.wantError {
				require.NoError(t, err)
				return
			}
			require.Error(t, err)
			require.NotContains(t, err.Error(), "secret")
			if tc.code != 202 {
				var admission *clientresizer.AdmissionError
				require.ErrorAs(t, err, &admission)
				require.Equal(t, 7*time.Second, admission.RetryAfter)
				require.Equal(t, tc.code == 503, admission.Retryable())
			}
		})
	}
}

func TestPollUsesSameTokenScopeAndChecksIdentity(t *testing.T) {
	id := outboxtypes.MustParse[outboxtypes.JobID]("9d435a36-6fad-4a52-a0a7-d474d3393ab3")
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Equal(t, http.MethodGet, r.Method)
		assert.Equal(t, "/jobs/"+id.String(), r.URL.Path)
		assert.Equal(t, "Bearer video-token", r.Header.Get("Authorization"))
		_, _ = io.WriteString(w, `{"job_id":"`+id.String()+`","idempotency_key":"42","status":"failed","artifacts":[]}`)
	}))
	defer server.Close()
	client := clientresizer.Must(clientresizer.NewOptions(
		server.Client(), server.URL+"/images/jobs", server.URL+"/jobs", logger.Discard(),
		clientresizer.WithVideoResizerToken("video-token"),
	))
	result, err := client.GetJob(context.Background(), clientresizer.JobRequest{JobID: id, TypeMedia: "video"})
	require.NoError(t, err)
	require.Equal(t, "failed", result.Status)
	require.Equal(t, "42", result.IdempotencyKey)
}
