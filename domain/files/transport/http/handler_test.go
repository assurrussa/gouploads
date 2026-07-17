package http_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	commonshared "github.com/assurrussa/goshared/pkg/filetypes"
	"github.com/assurrussa/goshared/pkg/logger"
	"github.com/assurrussa/goshared/pkg/pointer"
	"github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/assurrussa/goshared/pkg/tests"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared"
	uploadshandler "github.com/assurrussa/gouploads/domain/files/transport/http"
	uploadsmocks "github.com/assurrussa/gouploads/domain/files/transport/http/mocks"
	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

var _ suite.TestingSuite = (*HandlerSuite)(nil)

type HandlerSuite struct {
	suite.Suite

	ctrl *gomock.Controller

	handler  *uploadshandler.Handler
	fiberApp *fibertApp
	tusStore tusupload.Store

	mockTaskUploader *uploadsmocks.MockTaskUploader
	fileRepo         *uploadsmocks.MockFileRepository
}

type filePayload struct {
	ID           int64  `json:"id"`
	EntityID     int64  `json:"entityId"`
	Filename     string `json:"filename"`
	OriginalName string `json:"originalName"`
	FileType     string `json:"fileType"`
	MimeType     string `json:"mimeType"`
	Size         int64  `json:"size"`
	URL          string `json:"url"`
	PublicURL    string `json:"publicUrl"`
	ThumbnailURL string `json:"thumbnailUrl"`
	FullPath     string `json:"fullPath"`
	FolderPath   string `json:"folderPath"`
	SortOrder    int    `json:"sortOrder"`
	Status       string `json:"status"`
	IsPrimary    bool   `json:"isPrimary"`
	Width        int    `json:"width"`
	Height       int    `json:"height"`
	CreatedAt    string `json:"createdAt"`
	UpdatedAt    string `json:"updatedAt"`
	Data         *struct {
		Provider *struct {
			Driver string `json:"driver"`
		} `json:"provider"`
		Width  int    `json:"width"`
		Height int    `json:"height"`
		Alt    string `json:"alt"`
	} `json:"data"`
}

type listResponsePayload struct {
	Files    []filePayload `json:"files"`
	EntityID *int64        `json:"entityId,omitempty"`
}

type singleResponsePayload struct {
	File filePayload `json:"file"`
}

func decodeListResponse(t *testing.T, body string) listResponsePayload {
	t.Helper()
	var resp listResponsePayload
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	return resp
}

func decodeSingleResponse(t *testing.T, body string) singleResponsePayload {
	t.Helper()
	var resp singleResponsePayload
	require.NoError(t, json.Unmarshal([]byte(body), &resp))
	return resp
}

type fibertApp struct {
	*fiber.App
	t *testing.T
}

func newFibertApp(t *testing.T) *fibertApp {
	t.Helper()
	return &fibertApp{
		App: fiber.New(),
		t:   t,
	}
}

type fibertReqOpt func(*http.Request)

func fibertWithBody(r io.Reader) fibertReqOpt {
	return func(req *http.Request) {
		b, err := io.ReadAll(r)
		if err == nil {
			req.Body = io.NopCloser(bytes.NewReader(b))
			req.ContentLength = int64(len(b))
			req.GetBody = func() (io.ReadCloser, error) {
				return io.NopCloser(bytes.NewReader(b)), nil
			}
		}
	}
}

func fibertWithHeaders(headers map[string]string) fibertReqOpt {
	return func(req *http.Request) {
		for k, v := range headers {
			req.Header.Set(k, v)
		}
	}
}

func (f *fibertApp) AppSend(
	method string,
	_ sharedtypes.RequestID,
	handler fiber.Handler,
	opts ...fibertReqOpt,
) (*http.Response, string) {
	app := fiber.New()
	app.Add([]string{method}, "/", handler)

	req := httptest.NewRequestWithContext(context.Background(), method, "/", nil)
	for _, opt := range opts {
		opt(req)
	}

	resp, err := app.Test(req)
	require.NoError(f.t, err)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(f.t, err)
	// mock body close since the callers close it
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	return resp, string(bodyBytes)
}

func (f *fibertApp) CreateRequest(
	method string,
	_ sharedtypes.RequestID,
	handler fiber.Handler,
	opts ...fibertReqOpt,
) (*fiber.App, *http.Request) {
	app := fiber.New()
	app.Add([]string{method}, "/", handler)

	req := httptest.NewRequestWithContext(context.Background(), method, "/", nil)
	for _, opt := range opts {
		opt(req)
	}
	return app, req
}

func (f *fibertApp) Send(app *fiber.App, req *http.Request) (*http.Response, string) {
	resp, err := app.Test(req)
	require.NoError(f.t, err)

	bodyBytes, err := io.ReadAll(resp.Body)
	require.NoError(f.t, err)
	resp.Body = io.NopCloser(bytes.NewReader(bodyBytes))

	return resp, string(bodyBytes)
}

func (ts *HandlerSuite) assertFileMatchesCreateModel(payload filePayload) {
	ts.Require().EqualValues(201, payload.ID)
	ts.Require().EqualValues(134, payload.EntityID)
	ts.Require().Equal("sample-stored.png", payload.Filename)
	ts.Require().Equal("sample.png", payload.OriginalName)
	ts.Require().Equal("image/png", payload.MimeType)
	ts.Require().EqualValues(2048, payload.Size)
	ts.Require().Equal("https://ceph.localhost/admin/tmp/uploads/sample-stored.png", payload.URL)
	ts.Require().Equal(payload.URL, payload.PublicURL)
	ts.Require().Equal(payload.PublicURL, payload.ThumbnailURL)
	ts.Require().Equal("admin/tmp/uploads/sample-stored.png", payload.FullPath)
	ts.Require().Equal("admin/tmp/uploads", payload.FolderPath)
	ts.Require().Equal("queued", payload.Status)
	ts.Require().True(payload.IsPrimary)
	ts.Require().Equal(640, payload.Width)
	ts.Require().Equal(480, payload.Height)
	ts.Require().Equal("2024-01-02T03:04:05Z", payload.CreatedAt)
	ts.Require().Equal("2024-01-02T03:04:05Z", payload.UpdatedAt)
	ts.Require().NotNil(payload.Data)
	ts.Require().NotNil(payload.Data.Provider)
	ts.Require().Equal("local", payload.Data.Provider.Driver)
	ts.Require().Equal(640, payload.Data.Width)
	ts.Require().Equal(480, payload.Data.Height)
	ts.Require().Equal("Preview", payload.Data.Alt)
}

func NewHandlerSuite(t *testing.T) (context.Context, context.CancelFunc, *HandlerSuite) {
	t.Helper()

	return tests.NewSuite[*HandlerSuite](t, func(t *testing.T, _ context.Context) *HandlerSuite {
		t.Helper()

		ctrl := gomock.NewController(t)

		mockTaskUploader := uploadsmocks.NewMockTaskUploader(ctrl)
		mockFileRepo := uploadsmocks.NewMockFileRepository(ctrl)

		tusStore := mustTusStore(t.TempDir())
		log := logger.Discard()

		contextBuilder := func(_ context.Context, metadata map[string]string) (uploadstrategies.UploadContext, error) {
			return uploadstrategies.UploadContext{
				UserID:    101,
				UserUUID:  sharedtypes.NewUserID(),
				SessionID: "session-123",
				Metadata:  metadata,
			}, nil
		}

		urlComposer := func(path string) string {
			if strings.HasPrefix(path, "http") {
				return path
			}
			return "https://ceph.localhost/" + strings.TrimPrefix(path, "/")
		}

		handler := uploadshandler.NewHandler(
			mockTaskUploader,
			mockFileRepo,
			tusStore,
			log,
			contextBuilder,
			urlComposer,
		)

		fiberApp := newFibertApp(t)

		t.Cleanup(func() { _ = os.RemoveAll("public") })

		return &HandlerSuite{
			ctrl:             ctrl,
			handler:          handler,
			fiberApp:         fiberApp,
			tusStore:         tusStore,
			mockTaskUploader: mockTaskUploader,
			fileRepo:         mockFileRepo,
		}
	}, tests.WithIsParallel(false))
}

func mustTusStore(root string) tusupload.Store {
	store, err := tusupload.NewFileStore(root)
	if err != nil {
		panic(err)
	}
	return store
}

func TestUploads_Upload_Success(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	returned := []model.File{createModel()}

	ts.mockTaskUploader.EXPECT().UploadBatch(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req uploadservice.BatchRequest) ([]model.File, error) {
			ts.Require().Equal(shared.ObjectTypeExercise, req.ObjectType)
			ts.Require().Equal("55", req.ObjectID.String())
			ts.Require().NotEmpty(req.ManagerID)
			ts.Require().Len(req.FileHeaders, 1)
			return returned, nil
		},
	)

	body, contentType := buildMultipartBody(t, map[string]string{
		"objectType": shared.ObjectTypeExercise.String(),
		"objectId":   "55",
	}, "files", "sample.png", []byte("fake image"))

	resp, respBody := ts.fiberApp.AppSend(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.Upload(c) },
		fibertWithBody(body),
		fibertWithHeaders(map[string]string{"Content-Type": contentType}),
	)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusAccepted, resp.StatusCode)
	payload := decodeListResponse(ts.T(), respBody)
	ts.Require().Len(payload.Files, 1)
	ts.assertFileMatchesCreateModel(payload.Files[0])
}

func TestUploads_Upload_SingleSkipResizer(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	returned := createModel()

	ts.mockTaskUploader.EXPECT().UploadSingle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req uploadservice.SingleRequest) (model.File, error) {
			ts.Require().Equal(shared.ObjectTypeExercise, req.ObjectType)
			ts.Require().Equal("55", req.ObjectID.String())
			ts.Require().NotEmpty(req.ManagerID)
			ts.Require().NotNil(req.FileHeader)
			ts.Require().NotNil(req.Config)
			ts.True(req.Config.SkipResizer)
			return returned, nil
		},
	)

	body, contentType := buildMultipartBody(t, map[string]string{
		"objectType":  shared.ObjectTypeExercise.String(),
		"objectId":    "55",
		"file_type":   "video",
		"skip_resize": "on",
	}, "file", "sample.mp4", []byte("fake video"))

	resp, respBody := ts.fiberApp.AppSend(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.Upload(c) },
		fibertWithBody(body),
		fibertWithHeaders(map[string]string{"Content-Type": contentType}),
	)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusAccepted, resp.StatusCode)
	respPayload := decodeSingleResponse(ts.T(), respBody)
	ts.assertFileMatchesCreateModel(respPayload.File)
}

func TestUploads_Upload_MissingObjectType(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	const expPayload = `{"status":"error","error":"entity_type is required"}`
	ts.mockTaskUploader.EXPECT().UploadBatch(gomock.Any(), gomock.Any()).Times(0)

	resp, respBody := ts.fiberApp.AppSend(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.Upload(c) },
		fibertWithBody(strings.NewReader("")),
		fibertWithHeaders(map[string]string{"Content-Type": "application/x-www-form-urlencoded"}),
	)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusBadRequest, resp.StatusCode)
	ts.JSONEq(expPayload, respBody)
}

func TestUploads_UploadForRichText_Success(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	returned := createModel()

	ts.mockTaskUploader.EXPECT().UploadSingle(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req uploadservice.SingleRequest) (model.File, error) {
			ts.Require().Equal(shared.ObjectTypeAdmin, req.ObjectType)
			ts.Require().Equal("9", req.ObjectID.String())
			ts.Require().NotEmpty(req.ManagerID)
			ts.Require().NotNil(req.FileHeader)
			return returned, nil
		},
	)

	body, contentType := buildMultipartBody(t, map[string]string{
		"objectType": shared.ObjectTypeAdmin.String(),
		"objectId":   "9",
		"context":    "rich-text", // Simulate rich-text context
	}, "file", "cover.jpg", []byte("img data"))

	resp, respBody := ts.fiberApp.AppSend(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.Upload(c) },
		fibertWithBody(body),
		fibertWithHeaders(map[string]string{"Content-Type": contentType}),
	)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusAccepted, resp.StatusCode)
	respPayload := decodeSingleResponse(ts.T(), respBody)
	ts.assertFileMatchesCreateModel(respPayload.File)
}

func TestUploads_Upload_BatchError(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	const expPayload = `{"status":"error","error":"file is required"}`
	ts.mockTaskUploader.EXPECT().UploadBatch(gomock.Any(), gomock.Any()).Return(nil, uploadservice.ErrNoFiles)

	body, contentType := buildMultipartBody(t, map[string]string{
		"objectType": shared.ObjectTypeExercise.String(),
		"objectId":   "77",
	}, "files", "sample.png", []byte("fake image"))

	resp, respBody := ts.fiberApp.AppSend(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.Upload(c) },
		fibertWithBody(body),
		fibertWithHeaders(map[string]string{"Content-Type": contentType}),
	)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(fiber.StatusBadRequest, resp.StatusCode)
	ts.JSONEq(expPayload, respBody)
}

func TestUploads_Upload_BatchSkipResizer(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	returned := []model.File{createModel()}

	ts.mockTaskUploader.EXPECT().UploadBatch(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req uploadservice.BatchRequest) ([]model.File, error) {
			ts.Require().True(req.Config.SkipResizer)
			ts.Require().Len(req.FileHeaders, 1)
			ts.Require().Equal(shared.ObjectTypeExercise, req.ObjectType)
			return returned, nil
		},
	)

	body, contentType := buildMultipartBody(t, map[string]string{
		"objectType":  shared.ObjectTypeExercise.String(),
		"objectId":    "55",
		"file_type":   "video",
		"skip_resize": "on",
	}, "files", "sample.mp4", []byte("fake video"))

	resp, respBody := ts.fiberApp.AppSend(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.Upload(c) },
		fibertWithBody(body),
		fibertWithHeaders(map[string]string{"Content-Type": contentType}),
	)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusAccepted, resp.StatusCode)
	payload := decodeListResponse(ts.T(), respBody)
	ts.Require().Len(payload.Files, 1)
	ts.assertFileMatchesCreateModel(payload.Files[0])
}

func TestUploads_GetTask_SuccessWithFile(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	expected := createModel()
	ts.mockTaskUploader.EXPECT().GetFile(gomock.Any(), int64(15)).Return(expected, nil)

	reqID := sharedtypes.NewRequestID()
	app, req := ts.fiberApp.CreateRequest(
		http.MethodGet,
		reqID,
		func(c fiber.Ctx) error { return ts.handler.GetFile(c) },
	)
	req.URL.Path = "/files/upload/tasks/15"
	req.RequestURI = "/files/upload/tasks/15"
	app.Add([]string{http.MethodGet}, "/files/upload/tasks/:id", func(c fiber.Ctx) error {
		return ts.handler.GetFile(c)
	})

	resp, respBody := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(fiber.StatusOK, resp.StatusCode)
	got := decodeSingleResponse(ts.T(), respBody)
	ts.assertFileMatchesCreateModel(got.File)
}

func TestUploads_GetTask_NotFound(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	const expPayload = `{"status":"error","error":"task not found"}`
	ts.mockTaskUploader.EXPECT().GetFile(gomock.Any(), int64(404)).Return(model.File{}, uploadservice.ErrTaskNotFound)

	reqID := sharedtypes.NewRequestID()
	app, req := ts.fiberApp.CreateRequest(
		http.MethodGet,
		reqID,
		func(c fiber.Ctx) error { return ts.handler.GetFile(c) },
	)
	req.URL.Path = "/files/upload/tasks/404"
	req.RequestURI = "/files/upload/tasks/404"
	app.Add([]string{http.MethodGet}, "/files/upload/tasks/:id", func(c fiber.Ctx) error {
		return ts.handler.GetFile(c)
	})

	resp, respBody := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(fiber.StatusNotFound, resp.StatusCode)
	ts.JSONEq(expPayload, respBody)
}

func TestUploads_GetTask_InvalidID(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	const expPayload = `{"status":"error","error":"invalid task id"}`
	ts.mockTaskUploader.EXPECT().GetFile(gomock.Any(), gomock.Any()).Times(0)

	reqID := sharedtypes.NewRequestID()
	app, req := ts.fiberApp.CreateRequest(
		http.MethodGet,
		reqID,
		func(c fiber.Ctx) error { return ts.handler.GetFile(c) },
	)
	req.URL.Path = "/files/upload/tasks/bad"
	req.RequestURI = "/files/upload/tasks/bad"
	app.Add([]string{http.MethodGet}, "/files/upload/tasks/:id", func(c fiber.Ctx) error {
		return ts.handler.GetFile(c)
	})

	resp, respBody := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(fiber.StatusBadRequest, resp.StatusCode)
	ts.JSONEq(expPayload, respBody)
}

func TestUploads_DeleteFile_Success(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	const expPayload = `{"status":"pending","id":778,"entityId":99}`
	fileID := int64(778)
	entityID := shared.FileObjectID(99)
	ts.fileRepo.EXPECT().GetByID(gomock.Any(), fileID).Return(model.File{ID: fileID, ObjectID: &entityID}, nil)

	ts.mockTaskUploader.EXPECT().DeleteFile(gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req uploadservice.DeleteRequest) error {
			ts.Require().Equal(fileID, req.FileID)
			return nil
		},
	)

	app, req := ts.fiberApp.CreateRequest(
		http.MethodDelete,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.DeleteFile(c) },
	)
	app.Add([]string{http.MethodDelete}, "/files/:id", func(c fiber.Ctx) error {
		return ts.handler.DeleteFile(c)
	})
	req.URL.Path = "/files/778"
	req.URL.RawQuery = "confirm=true"
	req.RequestURI = "/files/778?confirm=true"
	resp, respBody := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusAccepted, resp.StatusCode)
	ts.JSONEq(expPayload, respBody)
}

func TestUploads_DeleteFile_MissingFileID(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	const expPayload = `{"status":"error","error":"file id is required"}`
	ts.mockTaskUploader.EXPECT().DeleteFile(gomock.Any(), gomock.Any()).Times(0)
	ts.fileRepo.EXPECT().GetByID(gomock.Any(), gomock.Any()).Times(0)

	resp, respBody := ts.fiberApp.AppSend(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.DeleteFile(c) },
		fibertWithBody(strings.NewReader("")),
		fibertWithHeaders(map[string]string{"Content-Type": "application/x-www-form-urlencoded"}),
	)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(fiber.StatusBadRequest, resp.StatusCode)
	ts.JSONEq(expPayload, respBody)
}

func TestUploads_DeleteFile_InvalidFileID(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	const expPayload = `{"status":"error","error":"invalid file id"}`
	ts.mockTaskUploader.EXPECT().DeleteFile(gomock.Any(), gomock.Any()).Times(0)
	ts.fileRepo.EXPECT().GetByID(gomock.Any(), gomock.Any()).Times(0)

	resp, respBody := ts.fiberApp.AppSend(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.DeleteFile(c) },
		fibertWithBody(strings.NewReader("fileId=oops")),
		fibertWithHeaders(map[string]string{"Content-Type": "application/x-www-form-urlencoded"}),
	)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(fiber.StatusBadRequest, resp.StatusCode)
	ts.JSONEq(expPayload, respBody)
}

func TestUploads_DeleteFile_ServiceError(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	const expPayload = `{"error":"failed to enqueue delete task", "status":"error"}`
	ts.fileRepo.EXPECT().GetByID(gomock.Any(), int64(321)).Return(model.File{ID: 321}, nil)
	ts.mockTaskUploader.EXPECT().DeleteFile(gomock.Any(), gomock.Any()).Return(errors.New("delete failure"))

	app, req := ts.fiberApp.CreateRequest(
		http.MethodDelete,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.DeleteFile(c) },
	)
	app.Add([]string{http.MethodDelete}, "/files/:id", func(c fiber.Ctx) error {
		return ts.handler.DeleteFile(c)
	})
	req.URL.Path = "/files/321"
	req.URL.RawQuery = "confirm=true"
	req.RequestURI = "/files/321?confirm=true"
	resp, respBody := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(fiber.StatusInternalServerError, resp.StatusCode)
	ts.JSONEq(expPayload, respBody)
}

func TestUploads_DeleteFile_RequiresConfirm(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	const expPayload = `{"status":"error","error":"confirmation required"}`
	ts.fileRepo.EXPECT().GetByID(gomock.Any(), gomock.Any()).Times(0)
	ts.mockTaskUploader.EXPECT().DeleteFile(gomock.Any(), gomock.Any()).Times(0)

	app, req := ts.fiberApp.CreateRequest(
		http.MethodDelete,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.DeleteFile(c) },
	)
	app.Add([]string{http.MethodDelete}, "/files/:id", func(c fiber.Ctx) error {
		return ts.handler.DeleteFile(c)
	})
	req.URL.Path = "/files/42"
	req.RequestURI = "/files/42"
	resp, respBody := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(fiber.StatusBadRequest, resp.StatusCode)
	ts.JSONEq(expPayload, respBody)
}

func TestUploads_ListFiles(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	entityID := shared.FileObjectID(11)
	ts.fileRepo.EXPECT().List(gomock.Any(), gomock.Any()).Return([]model.File{
		{
			ID:               5,
			OriginalFileName: "preview.png",
			MimeType:         "image/png",
			URL:              "https://cdn/test.png",
			ObjectID:         &entityID,
			Position:         2,
		},
	}, 1, nil)

	app, req := ts.fiberApp.CreateRequest(
		http.MethodGet,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.ListFiles(c) },
	)
	app.Add([]string{http.MethodGet}, "/files", func(c fiber.Ctx) error { return ts.handler.ListFiles(c) })
	req.URL.Path = "/files"
	req.URL.RawQuery = "entity_type=" + shared.ObjectTypeExercise.String() + "&entity_id=11"
	req.RequestURI = "/files?" + req.URL.RawQuery
	resp, respBody := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(fiber.StatusOK, resp.StatusCode)
	respPayload := decodeListResponse(ts.T(), respBody)
	ts.Require().Len(respPayload.Files, 1)
	ts.Require().NotNil(respPayload.EntityID)
	if respPayload.EntityID != nil {
		ts.Require().EqualValues(11, *respPayload.EntityID)
	}
	file := respPayload.Files[0]
	ts.Require().EqualValues(5, file.ID)
	ts.Require().EqualValues(11, file.EntityID)
	ts.Require().Equal("preview.png", file.OriginalName)
	ts.Require().Equal("image/png", file.MimeType)
	ts.Require().Equal("https://cdn/test.png", file.URL)
	ts.Require().Equal("https://cdn/test.png", file.PublicURL)
	ts.Require().Equal(2, file.SortOrder)
}

// --- TUS Tests ---

func TestUploads_TusCreate_AcceptsHostOwnedObjectType(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)

	metadata := map[string]string{
		"filename":    "test.png",
		"entity_type": shared.FileObjectType("post").String(),
		"entity_id":   "123",
	}
	encodedMetadata := encodeTusMetadata(metadata)

	reqID := sharedtypes.NewRequestID()
	app, req := ts.fiberApp.CreateRequest(
		http.MethodPost,
		reqID,
		func(c fiber.Ctx) error { return ts.handler.TusCreate(c) },
	)
	req.Header.Set("Tus-Resumable", tusupload.Version)
	req.Header.Set("Upload-Length", "100")
	req.Header.Set("Upload-Metadata", encodedMetadata)

	resp, _ := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusCreated, resp.StatusCode)
	ts.Require().NotEmpty(resp.Header.Get("Location"))
}

func TestUploads_CMSTusCreateAndPatchDoNotRequireGenericEntity(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)
	ts.handler.RegisterStrategy("cms", cmsOnlyUploadStrategy{})

	metadata := encodeTusMetadata(map[string]string{
		"filename":  "cms.png",
		"context":   "cms",
		"file_type": "image",
	})
	app, req := ts.fiberApp.CreateRequest(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.TusCreateCMS(c) },
	)
	req.Header.Set("Tus-Resumable", tusupload.Version)
	req.Header.Set("Upload-Length", "8")
	req.Header.Set("Upload-Metadata", metadata)

	resp, _ := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })
	ts.Require().Equal(http.StatusCreated, resp.StatusCode)
	uploadID := path.Base(resp.Header.Get("Location"))
	ts.Require().NotEmpty(uploadID)

	patchApp := fiber.New()
	patchApp.Patch("/tus/:id", ts.handler.TusPatchCMS)
	req = httptest.NewRequestWithContext(
		t.Context(),
		http.MethodPatch,
		"/tus/"+uploadID,
		bytes.NewReader([]byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}),
	)
	req.Header.Set("Tus-Resumable", tusupload.Version)
	req.Header.Set("Content-Type", tusupload.ContentType)
	req.Header.Set("Upload-Offset", "0")

	resp, err := patchApp.Test(req)
	ts.Require().NoError(err)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })
	ts.Require().Equal(http.StatusNoContent, resp.StatusCode)

	session, err := ts.tusStore.Get(context.Background(), uploadID)
	ts.Require().NoError(err)
	ts.Require().Equal("cms", session.Metadata["context"])
	ts.Require().NotContains(session.Metadata, "entity_type")
	ts.Require().NotContains(session.Metadata, "entity_id")
}

func TestUploads_CMSTusCreateFailsClosedWhenStrategyReturnsNilConfig(t *testing.T) {
	_, _, ts := NewHandlerSuite(t)
	ts.handler.RegisterStrategy("cms", nilConfigUploadStrategy{})

	metadata := encodeTusMetadata(map[string]string{
		"filename":  "cms.png",
		"context":   "cms",
		"file_type": "image",
	})
	app, req := ts.fiberApp.CreateRequest(
		http.MethodPost,
		sharedtypes.NewRequestID(),
		func(c fiber.Ctx) error { return ts.handler.TusCreateCMS(c) },
	)
	req.Header.Set("Tus-Resumable", tusupload.Version)
	req.Header.Set("Upload-Length", "8")
	req.Header.Set("Upload-Metadata", metadata)

	resp, _ := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })
	ts.Require().Equal(http.StatusInternalServerError, resp.StatusCode)
}

type cmsOnlyUploadStrategy struct{}

type nilConfigUploadStrategy struct{ cmsOnlyUploadStrategy }

func (nilConfigUploadStrategy) GetConfig(context.Context, uploadstrategies.UploadContext) *uploadservice.FileUploadConfig {
	return nil
}

func (cmsOnlyUploadStrategy) CanUpload(context.Context, uploadstrategies.UploadContext) error {
	return nil
}

func (cmsOnlyUploadStrategy) GetConfig(context.Context, uploadstrategies.UploadContext) *uploadservice.FileUploadConfig {
	return &uploadservice.FileUploadConfig{
		MaxFileSize:       10 * 1024 * 1024,
		AllowedExtensions: []string{".png"},
		AllowedMimeTypes:  map[string][]string{".png": {"image/png"}},
	}
}

func (cmsOnlyUploadStrategy) GetAfterJobs(context.Context, uploadstrategies.UploadContext) ([]shared.FileEventAfterJob, error) {
	return nil, nil
}

func TestUploads_TusHead_Success(t *testing.T) {
	ctx, _, ts := NewHandlerSuite(t)

	// Create a dummy session directly in store
	session, err := ts.tusStore.Create(ctx, tusupload.CreateRequest{
		UploadLength: 100,
		FileName:     "test.png",
		OriginalName: "test.png",
		OwnerID:      101, // Matches mocked context
	})
	ts.Require().NoError(err)

	reqID := sharedtypes.NewRequestID()
	app, req := ts.fiberApp.CreateRequest(
		http.MethodHead,
		reqID,
		func(c fiber.Ctx) error { return ts.handler.TusHead(c) },
	)
	app.Add([]string{http.MethodHead}, "/tus/:id", func(c fiber.Ctx) error { return ts.handler.TusHead(c) })
	req.URL.Path = "/tus/" + session.ID
	req.RequestURI = "/tus/" + session.ID
	req.Header.Set("Tus-Resumable", tusupload.Version)

	resp, _ := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusOK, resp.StatusCode)
	ts.Require().Equal("0", resp.Header.Get("Upload-Offset"))
	ts.Require().Equal("100", resp.Header.Get("Upload-Length"))
}

func TestUploads_TusPatch_Success(t *testing.T) {
	ctx, _, ts := NewHandlerSuite(t)

	metadata := map[string]string{
		"filename":    "test.png",
		"entity_type": shared.ObjectTypeExercise.String(),
		"entity_id":   "123",
	}
	// Create session
	session, err := ts.tusStore.Create(ctx, tusupload.CreateRequest{
		UploadLength: 8,
		FileName:     "test.png",
		OriginalName: "test.png",
		OwnerID:      101,
		Metadata:     metadata,
	})
	ts.Require().NoError(err)

	reqID := sharedtypes.NewRequestID()
	// Fake PNG body to pass mime check
	pngHeader := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	app, req := ts.fiberApp.CreateRequest(
		http.MethodPatch,
		reqID,
		func(c fiber.Ctx) error { return ts.handler.TusPatch(c) },
		fibertWithBody(bytes.NewReader(pngHeader)),
	)
	app.Add([]string{http.MethodPatch}, "/tus/:id", func(c fiber.Ctx) error { return ts.handler.TusPatch(c) })
	req.URL.Path = "/tus/" + session.ID
	req.RequestURI = "/tus/" + session.ID

	req.Header.Set("Tus-Resumable", tusupload.Version)
	req.Header.Set("Content-Type", tusupload.ContentType)
	req.Header.Set("Upload-Offset", "0")

	resp, _ := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusNoContent, resp.StatusCode)
	ts.Require().Equal(strconv.FormatInt(int64(len(pngHeader)), 10), resp.Header.Get("Upload-Offset"))
}

func TestUploads_TusComplete_Success(t *testing.T) {
	ctx, _, ts := NewHandlerSuite(t)

	metadata := map[string]string{
		"filename":    "test.png",
		"entity_type": shared.ObjectTypeExercise.String(),
		"entity_id":   "123",
	}
	// Create session
	session, err := ts.tusStore.Create(ctx, tusupload.CreateRequest{
		UploadLength: 5,
		FileName:     "test.png",
		OriginalName: "test.png",
		OwnerID:      101,
		Metadata:     metadata,
	})
	ts.Require().NoError(err)
	// Append data to complete it
	_, err = ts.tusStore.Append(ctx, session.ID, 0, []byte("hello"), "image/png")
	ts.Require().NoError(err)

	// Mock TaskUploader expectation
	returned := createModel()
	ts.mockTaskUploader.EXPECT().UploadReader(gomock.Any(), gomock.Any(), gomock.Any()).DoAndReturn(
		func(_ context.Context, req uploadservice.ReaderRequest, input uploadservice.ReaderUploadInput) (model.File, error) {
			ts.Require().Equal(shared.ObjectTypeExercise, req.ObjectType)
			ts.Require().Equal("123", req.ObjectID.String())
			ts.Require().Equal(int64(5), input.Size)
			return returned, nil
		},
	)

	reqID := sharedtypes.NewRequestID()
	app, req := ts.fiberApp.CreateRequest(
		http.MethodPost,
		reqID,
		func(c fiber.Ctx) error { return ts.handler.TusComplete(c) },
	)
	app.Add([]string{http.MethodPost}, "/tus/:id/complete", func(c fiber.Ctx) error { return ts.handler.TusComplete(c) })
	req.URL.Path = "/tus/" + session.ID + "/complete"
	req.RequestURI = "/tus/" + session.ID + "/complete"

	req.Header.Set("Tus-Resumable", tusupload.Version)

	resp, respBody := ts.fiberApp.Send(app, req)
	ts.T().Cleanup(func() { ts.Require().NoError(resp.Body.Close()) })

	ts.Require().Equal(http.StatusAccepted, resp.StatusCode)
	respPayload := decodeSingleResponse(ts.T(), respBody)
	ts.assertFileMatchesCreateModel(respPayload.File)
}

func encodeTusMetadata(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for k, v := range m {
		encoded := base64.StdEncoding.EncodeToString([]byte(v))
		parts = append(parts, fmt.Sprintf("%s %s", k, encoded))
	}
	return strings.Join(parts, ",")
}

func buildMultipartBody(
	t *testing.T,
	fields map[string]string,
	fieldName, fileName string,
	fileData []byte,
) (*bytes.Buffer, string) {
	t.Helper()

	var body bytes.Buffer
	writer := multipart.NewWriter(&body)

	for key, value := range fields {
		require.NoError(t, writer.WriteField(key, value))
	}

	part, err := writer.CreateFormFile(fieldName, fileName)
	require.NoError(t, err)
	_, err = io.Copy(part, bytes.NewReader(fileData))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	return &body, writer.FormDataContentType()
}

func createModel() model.File {
	tm := time.Date(2024, time.January, 2, 3, 4, 5, 0, time.UTC)

	return model.File{
		ID:               201,
		ObjectID:         pointer.To(shared.FileObjectID(134)),
		FileType:         commonshared.FileTypeImage,
		Position:         124345,
		OriginalFileName: "sample.png",
		FileName:         "sample-stored.png",
		FolderPath:       "admin/tmp/uploads",
		MimeType:         "image/png",
		Size:             2048,
		URL:              "/admin/tmp/uploads/sample-stored.png",
		IsPrimary:        true,
		CreatedAt:        tm,
		UpdatedAt:        tm,
		Data: &model.FileData{
			Provider: model.ProviderMetadata{Driver: "local"},
			Width:    640,
			Height:   480,
			Alt:      "Preview",
			Uploader: shared.FileUploader{
				Status: shared.FileUploadTaskStatusQueued,
			},
		},
	}
}
