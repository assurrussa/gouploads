package clientresizer_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"testing"

	"github.com/assurrussa/goshared/pkg/logger"
	"github.com/assurrussa/goshared/pkg/tests"
	transporthttp "github.com/assurrussa/outbox/infrastructure/transport/http"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/assert"
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
