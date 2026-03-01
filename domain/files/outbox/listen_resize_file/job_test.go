package listenresizefilejob_test

import (
	"context"
	"errors"
	"testing"

	"github.com/assurrussa/goshared/pkg/logger"
	"github.com/assurrussa/goshared/pkg/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	listenresizefilejob "github.com/assurrussa/gouploads/domain/files/outbox/listen_resize_file"
	listenresizefilejobmocks "github.com/assurrussa/gouploads/domain/files/outbox/listen_resize_file/mocks"
	usecase "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
)

type TestSuite struct {
	suite.Suite

	useCaseMock *listenresizefilejobmocks.MocklistenResizeFileUseCase

	job         *listenresizefilejob.Job
	expectError error
}

func NewTestSuite(t *testing.T) (context.Context, context.CancelFunc, *TestSuite) {
	t.Helper()

	return tests.NewSuite[*TestSuite](t, func(t *testing.T, _ context.Context) *TestSuite {
		t.Helper()

		log := logger.Discard()

		ctrl := gomock.NewController(t)
		useCaseMock := listenresizefilejobmocks.NewMocklistenResizeFileUseCase(ctrl)

		job := listenresizefilejob.Must(listenresizefilejob.NewOptions(useCaseMock, log))

		return &TestSuite{
			job:         job,
			useCaseMock: useCaseMock,
			expectError: errors.New("test error"),
		}
	})
}

func TestJob_MustInit(t *testing.T) {
	assert.Panics(t, func() {
		listenresizefilejob.Must(listenresizefilejob.NewOptions(nil, nil))
	})
}

func TestJobHandle_Success(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	ts.Equal(listenresizefilejob.JobName, ts.job.Name())

	taskID := int64(123)
	payload := listenresizefilejob.NewPayload(
		taskID,
		"done",
		"",
		map[string]any{"file_id": "123"},
		[]usecase.Artifact{
			{Preset: "medium", URL: "https://ceph.example.com/image.png"},
			{Preset: "small", URL: "https://ceph.example.com/small.png"},
		},
	)
	payloadBytes, err := listenresizefilejob.MarshalPayload(payload)
	ts.Require().NoError(err)

	ts.useCaseMock.EXPECT().Handle(ctx, usecase.Request{
		ExternalID: payload.ExternalID,
		Status:     payload.State,
		Error:      payload.Error,
		Metadata:   map[string]any{"file_id": "123"},
		Artifacts:  payload.Artifacts,
	}).Return(usecase.Response{}, nil).Times(1)

	err = ts.job.Handle(ctx, payloadBytes)
	ts.Require().NoError(err)
}

func TestJobHandle_Error_UseCase(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	taskID := int64(123)
	payload := listenresizefilejob.NewPayload(
		taskID,
		"done",
		"",
		map[string]any{"file_id": "123"},
		[]usecase.Artifact{
			{Preset: "medium", URL: "https://ceph.example.com/image.png"},
			{Preset: "small", URL: "https://ceph.example.com/small.png"},
		},
	)
	payloadBytes, err := listenresizefilejob.MarshalPayload(payload)
	ts.Require().NoError(err)

	ts.useCaseMock.EXPECT().Handle(ctx, usecase.Request{
		ExternalID: payload.ExternalID,
		Status:     payload.State,
		Error:      payload.Error,
		Metadata:   map[string]any{"file_id": "123"},
		Artifacts:  payload.Artifacts,
	}).Return(usecase.Response{}, ts.expectError).Times(1)

	err = ts.job.Handle(ctx, payloadBytes)
	ts.Require().ErrorIs(err, ts.expectError)
}

func TestJobHandle_Error_payload(t *testing.T) {
	ctx, _, ts := NewTestSuite(t)

	err := ts.job.Handle(ctx, `{asfasfqwer:""`)
	ts.Require().Error(err)
}
