package clientresizer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"sync/atomic"
	"testing"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/goshared/pkg/tests"
	transporthttp "github.com/assurrussa/goshared/pkg/transport/http"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	clientresizermocks "github.com/assurrussa/gouploads/domain/files/service/client_resizer/mocks"
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

	httpReq := transporthttp.Request{
		ExpectStatusCode: http.StatusAccepted,
		Method:           fiber.MethodPost,
		URL:              ts.urlImage,
		Body:             bytes.NewReader(data),
		Headers: map[string]string{
			fiber.HeaderContentType: fiber.MIMEApplicationJSONCharsetUTF8,
		},
	}
	respExp := `{"job_id": "9d435a36-6fad-4a52-a0a7-d474d3393ab3", "status": "queued"}`
	ts.httpClientMock.EXPECT().DoWithRequestAndParse(ctx, httpReq, gomock.Any()).
		DoAndReturn(func(_ context.Context, _ transporthttp.Request, data any) error {
			ts.Require().NoError(json.Unmarshal([]byte(respExp), data))

			return nil
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

	ts.httpClientMock.EXPECT().DoWithRequestAndParse(ctx, gomock.Any(), gomock.Any()).
		DoAndReturn(func(_ context.Context, _ transporthttp.Request, _ any) error {
			return ts.errExpect
		}).Times(1)

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
