package clientresizer_test

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	clientresizermocks "github.com/assurrussa/gouploads/domain/files/service/client_resizer/mocks"
	tests "github.com/assurrussa/gouploads/internal/testsupport"
)

type TestSuite struct {
	suite.Suite

	httpClientMock *clientresizermocks.MockhttpClient

	client    *clientresizer.Service
	urlImage  string
	urlVideo  string
	errExpect error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		httpClientMock := clientresizermocks.NewMockhttpClient(ctrl)

		client := clientresizer.Must(clientresizer.NewOptions(
			httpClientMock,
			"https://resizer.example.com/image",
			"https://resizer.example.com/video",
			log,
		))

		return &TestSuite{
			client:         client,
			httpClientMock: httpClientMock,
			errExpect:      errors.New("expected error"),
			urlImage:       "https://resizer.example.com/image",
			urlVideo:       "https://resizer.example.com/image",
		}
	})
}

func TestHandle_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		clientresizer.Must(clientresizer.NewOptions(nil, "", "", nil))
	})
}

func TestHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	// Arrange.
	data := []byte("data")

	respExp := `{"job_id": "9d435a36-6fad-4a52-a0a7-d474d3393ab3", "status": "queued"}`
	ts.httpClientMock.EXPECT().Do(gomock.Any()).DoAndReturn(func(r *http.Request) (*http.Response, error) {
		ts.Equal(ctx, r.Context())
		ts.Equal(http.MethodPost, r.Method)
		ts.Equal(ts.urlImage, r.URL.String())
		ts.Equal(fiber.MIMEApplicationJSONCharsetUTF8, r.Header.Get(fiber.HeaderContentType))
		body, err := io.ReadAll(r.Body)
		ts.Require().NoError(err)
		ts.Equal(data, body)
		return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(respExp))}, nil
	}).Times(1)

	// Action.
	resp, err := ts.client.SendResize(ctx, clientresizer.Request{
		TypeMedia: "image",
		Data:      data,
	})

	// Assertion.
	ts.Require().NoError(err)
	ts.Equal("9d435a36-6fad-4a52-a0a7-d474d3393ab3", resp.JobID.String())
	ts.Equal("queued", resp.Status)
}

func TestHandle_Error(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	// Arrange.
	data := []byte("data")

	ts.httpClientMock.EXPECT().Do(gomock.Any()).Return(nil, ts.errExpect).Times(1)

	// Action.
	resp, err := ts.client.SendResize(ctx, clientresizer.Request{
		TypeMedia: "video",
		Data:      data,
	})

	// Assertion.
	ts.Require().ErrorIs(err, ts.errExpect)
	ts.Empty(resp)
}

func TestDownloadFileStreamsSignedArtifactWithoutSecretHeaders(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		assert.Empty(t, r.Header.Get(fiber.HeaderAuthorization))
		assert.Empty(t, r.Header.Get("X-API-Token"))
		assert.Equal(t, "signed-value", r.URL.Query().Get("X-Amz-Signature"))
		w.Header().Set(fiber.HeaderContentType, "image/png")
		_, _ = io.WriteString(w, "streamed-artifact")
	}))
	t.Cleanup(server.Close)

	ctrl := gomock.NewController(t)
	service := clientresizer.Must(clientresizer.NewOptions(
		clientresizermocks.NewMockhttpClient(ctrl),
		server.URL+"/jobs",
		server.URL+"/jobs",
		logger.Discard(),
		clientresizer.WithImageResizerToken("must-not-leak"),
		clientresizer.WithArtifactClient(server.Client()),
	))

	response, err := service.DownloadFile(t.Context(), clientresizer.RequestDownload{
		Preset:    "main",
		URL:       server.URL + "/artifacts/main.png?X-Amz-Signature=signed-value",
		TypeMedia: "image",
	})
	require.NoError(t, err)
	defer func() { require.NoError(t, response.Body.Close()) }()
	body, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	assert.Equal(t, "streamed-artifact", string(body))
	assert.Equal(t, "image/png", response.ContentType)
}

func TestDownloadFileRejectsWrongOrigin(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	service := clientresizer.Must(clientresizer.NewOptions(
		clientresizermocks.NewMockhttpClient(ctrl),
		"https://resizer.example.test/jobs",
		"https://resizer.example.test/jobs",
		logger.Discard(),
	))

	_, err := service.DownloadFile(t.Context(), clientresizer.RequestDownload{
		Preset:    "main",
		URL:       "https://attacker.example.test/artifact?token=secret",
		TypeMedia: "image",
	})
	require.ErrorContains(t, err, "does not match")
	assert.NotContains(t, err.Error(), "token=secret")
}

func TestDownloadFileRedactsSignedQueryFromErrors(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "artifact unavailable", http.StatusBadGateway)
	}))
	t.Cleanup(server.Close)

	ctrl := gomock.NewController(t)
	service := clientresizer.Must(clientresizer.NewOptions(
		clientresizermocks.NewMockhttpClient(ctrl),
		server.URL+"/jobs",
		server.URL+"/jobs",
		logger.Discard(),
		clientresizer.WithArtifactClient(server.Client()),
	))

	_, err := service.DownloadFile(t.Context(), clientresizer.RequestDownload{
		Preset:    "main",
		URL:       server.URL + "/artifact?X-Amz-Signature=top-secret",
		TypeMedia: "image",
	})
	require.Error(t, err)
	assert.NotContains(t, err.Error(), "top-secret")
	assert.NotContains(t, err.Error(), "X-Amz-Signature")
}

func TestDownloadFileRedactsSignedQueryFromTransportErrors(t *testing.T) {
	t.Parallel()

	ctrl := gomock.NewController(t)
	artifactClient := &failingArtifactClient{err: &url.Error{
		Op:  http.MethodGet,
		URL: "https://resizer.example.test/artifact?X-Amz-Signature=top-secret",
		Err: errors.New("connection failed"),
	}}
	service := clientresizer.Must(clientresizer.NewOptions(
		clientresizermocks.NewMockhttpClient(ctrl),
		"https://resizer.example.test/jobs",
		"https://resizer.example.test/jobs",
		logger.Discard(),
		clientresizer.WithArtifactClient(artifactClient),
	))

	_, err := service.DownloadFile(t.Context(), clientresizer.RequestDownload{
		Preset:    "main",
		URL:       "https://resizer.example.test/artifact?X-Amz-Signature=top-secret",
		TypeMedia: "image",
	})
	require.Error(t, err)
	assert.Contains(t, err.Error(), "connection failed")
	assert.NotContains(t, err.Error(), "top-secret")
	assert.NotContains(t, err.Error(), "X-Amz-Signature")
}

func TestDownloadFileDoesNotFollowArtifactRedirects(t *testing.T) {
	t.Parallel()

	var redirectReached atomic.Bool
	redirectTarget := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		redirectReached.Store(true)
	}))
	t.Cleanup(redirectTarget.Close)

	artifactServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, redirectTarget.URL+"/stolen", http.StatusFound)
	}))
	t.Cleanup(artifactServer.Close)

	ctrl := gomock.NewController(t)
	service := clientresizer.Must(clientresizer.NewOptions(
		clientresizermocks.NewMockhttpClient(ctrl),
		artifactServer.URL+"/jobs",
		artifactServer.URL+"/jobs",
		logger.Discard(),
	))

	_, err := service.DownloadFile(t.Context(), clientresizer.RequestDownload{
		Preset:    "main",
		URL:       artifactServer.URL + "/artifact?X-Amz-Signature=top-secret",
		TypeMedia: "image",
	})
	require.ErrorContains(t, err, "unexpected status 302")
	assert.NotContains(t, err.Error(), "top-secret")
	assert.False(t, redirectReached.Load())
}

type failingArtifactClient struct {
	err error
}

func (c *failingArtifactClient) Do(*http.Request) (*http.Response, error) {
	return nil, c.err
}

func TestSendResizeHTTPContract(t *testing.T) {
	t.Parallel()
	for _, status := range []int{http.StatusAccepted, http.StatusOK, http.StatusInternalServerError} {
		t.Run(http.StatusText(status), func(t *testing.T) {
			t.Parallel()
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				assert.Equal(t, http.MethodPost, r.Method)
				assert.Equal(t, "/video", r.URL.Path)
				assert.Equal(t, "Bearer video-secret", r.Header.Get(fiber.HeaderAuthorization))
				assert.Equal(t, "video-secret", r.Header.Get("X-API-Token"))
				assert.Equal(t, fiber.MIMEApplicationJSONCharsetUTF8, r.Header.Get(fiber.HeaderContentType))
				w.WriteHeader(status)
				_, _ = io.WriteString(w, `{"job_id":"9d435a36-6fad-4a52-a0a7-d474d3393ab3","status":"queued"}`)
			}))
			t.Cleanup(server.Close)
			service := clientresizer.Must(clientresizer.NewOptions(
				server.Client(), server.URL+"/image", server.URL+"/video", logger.Discard(),
				clientresizer.WithTokenProvider(func(context.Context) (string, string) {
					return "image-secret", "video-secret"
				}),
			))
			response, err := service.SendResize(t.Context(), clientresizer.Request{TypeMedia: "video", Data: []byte(`{}`)})
			if status == http.StatusAccepted {
				require.NoError(t, err)
				assert.Equal(t, "queued", response.Status)
			} else {
				require.ErrorContains(t, err, "unexpected status")
				assert.Empty(t, response)
			}
		})
	}
}

func TestSendResizeCancellationAndInvalidJSON(t *testing.T) {
	t.Parallel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusAccepted)
		_, _ = io.WriteString(w, "invalid json")
	}))
	t.Cleanup(server.Close)
	service := clientresizer.Must(clientresizer.NewOptions(server.Client(), server.URL, server.URL, logger.Discard()))
	_, err := service.SendResize(t.Context(), clientresizer.Request{TypeMedia: "image", Data: []byte(`{}`)})
	require.ErrorContains(t, err, "decode resize response")
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = service.SendResize(ctx, clientresizer.Request{TypeMedia: "image", Data: []byte(`{}`)})
	require.ErrorIs(t, err, context.Canceled)
}
