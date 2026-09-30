package http_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path"
	"strconv"
	"strings"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/gofiber/fiber/v3"
	"github.com/stretchr/testify/require"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/repositories/filerepo"
	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared"
	uploadhttp "github.com/assurrussa/gouploads/domain/files/transport/http"
	mocks "github.com/assurrussa/gouploads/domain/files/transport/http/mocks"
	"github.com/assurrussa/gouploads/internal/identity"
	"github.com/assurrussa/gouploads/shared/uploadstrategies"
)

type fixture struct {
	handler  *uploadhttp.Handler
	uploader *mocks.MockTaskUploader
	repo     *mocks.MockFileRepository
	store    tusupload.Store
	owner    identity.UserID
}

func newFixture(t *testing.T) *fixture {
	t.Helper()
	ctrl := gomock.NewController(t)
	uploader := mocks.NewMockTaskUploader(ctrl)
	repo := mocks.NewMockFileRepository(ctrl)
	store, err := tusupload.NewFileStore(t.TempDir())
	require.NoError(t, err)
	owner := identity.NewUserID()
	handler := uploadhttp.NewHandlerWithPolicy(uploader, repo, store, logger.Discard(),
		func(_ context.Context, meta map[string]string) (uploadstrategies.UploadContext, error) {
			return uploadstrategies.UploadContext{UserID: 101, UserUUID: owner, Metadata: meta}, nil
		},
		func(p string) string {
			if strings.HasPrefix(p, "http") {
				return p
			}
			return "https://media.example.test/" + strings.TrimLeft(p, "/")
		},
		uploadhttp.HandlerPolicy{AllowDefaultStrategy: true, TrustRouteGuards: true})
	handler.RegisterStrategy("rich-text", imageStrategy{})
	handler.RegisterStrategy("cms", imageStrategy{})
	return &fixture{handler, uploader, repo, store, owner}
}

type testResponse struct {
	StatusCode int
	Header     http.Header
}

func send(
	t *testing.T,
	method, pattern, target string,
	handler fiber.Handler,
	body io.Reader,
	headers map[string]string,
) (testResponse, []byte) {
	t.Helper()
	app := fiber.New()
	app.Add([]string{method}, pattern, handler)
	req := httptest.NewRequestWithContext(context.Background(), method, target, body)
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := app.Test(req)
	require.NoError(t, err)
	data, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.NoError(t, resp.Body.Close())
	return testResponse{StatusCode: resp.StatusCode, Header: resp.Header.Clone()}, data
}

func multipartBody(t *testing.T, fields map[string]string, field string) (*bytes.Buffer, string) {
	t.Helper()
	var body bytes.Buffer
	writer := multipart.NewWriter(&body)
	for k, v := range fields {
		require.NoError(t, writer.WriteField(k, v))
	}
	part, err := writer.CreateFormFile(field, "source.png")
	require.NoError(t, err)
	_, err = part.Write([]byte("test bytes"))
	require.NoError(t, err)
	require.NoError(t, writer.Close())
	return &body, writer.FormDataContentType()
}

func metadataHeader(values map[string]string) string {
	parts := make([]string, 0, len(values))
	for k, v := range values {
		parts = append(parts, k+" "+base64.StdEncoding.EncodeToString([]byte(v)))
	}
	return strings.Join(parts, ",")
}

func modelFile() model.File {
	objectID := shared.FileObjectID(134)
	stamp := time.Date(2024, 1, 2, 3, 4, 5, 0, time.UTC)
	return model.File{
		ID: 201, ObjectType: "post", ObjectID: &objectID, FileType: model.FileTypeImage, Position: 2,
		OriginalFileName: "source.png", FileName: "stored.png", FolderPath: "tmp/uploads/post/134", MimeType: "image/png", Size: 2048,
		URL: "/tmp/uploads/post/134/stored.png", IsPrimary: true, CreatedAt: stamp, UpdatedAt: stamp,
		Data: &model.FileData{
			Provider: model.ProviderMetadata{Driver: "local"},
			Width:    640,
			Height:   480,
			Alt:      "Preview",
			Uploader: shared.FileUploader{Status: shared.FileUploadTaskStatusQueued},
		},
	}
}

func responseFile(t *testing.T, data []byte) map[string]any {
	t.Helper()
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	file, ok := result["file"].(map[string]any)
	require.True(t, ok)
	return file
}

func assertQueued(t *testing.T, file map[string]any) {
	t.Helper()
	require.EqualValues(t, 201, file["id"])
	require.EqualValues(t, 134, file["entityId"])
	require.Equal(t, "stored.png", file["filename"])
	require.Equal(t, "source.png", file["originalName"])
	require.Equal(t, "image/png", file["mimeType"])
	require.EqualValues(t, 2048, file["size"])
	require.Equal(t, true, file["isPrimary"])
	require.Equal(t, "queued", file["status"])
	require.Empty(t, file["url"])
	require.Empty(t, file["publicUrl"])
	require.Empty(t, file["fullPath"])
	require.Empty(t, file["folderPath"])
	require.EqualValues(t, 640, file["width"])
	require.EqualValues(t, 480, file["height"])
	require.Equal(t, "2024-01-02T03:04:05Z", file["createdAt"])
	data, ok := file["data"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "Preview", data["alt"])
	provider, ok := data["provider"].(map[string]any)
	require.True(t, ok)
	require.Equal(t, "local", provider["driver"])
}

func TestUploadTransportContracts(t *testing.T) {
	for _, field := range []string{"file", "files"} {
		for _, contextName := range []string{"", "rich-text"} {
			t.Run(field+"/"+contextName, func(t *testing.T) {
				f := newFixture(t)
				fields := map[string]string{"objectType": "post", "objectId": "55", "context": contextName, "skip_resize": "on"}
				if field == "file" {
					f.uploader.EXPECT().UploadSingle(gomock.Any(),
						gomock.Any()).DoAndReturn(func(_ context.Context,
						req uploadservice.SingleRequest,
					) (model.File, error) {
						require.Equal(t, int64(101), req.ManagerID)
						require.Equal(t, f.owner, req.UploaderUUID)
						require.Equal(t, "post", req.ObjectType.String())
						require.EqualValues(t, 55, req.ObjectID)
						require.True(t, req.Config.SkipResizer)
						require.NotNil(t, req.FileHeader)
						return modelFile(), nil
					})
				} else {
					f.uploader.EXPECT().UploadBatch(gomock.Any(),
						gomock.Any()).DoAndReturn(func(_ context.Context,
						req uploadservice.BatchRequest,
					) ([]model.File, error) {
						require.Equal(t, int64(101), req.ManagerID)
						require.Len(t, req.FileHeaders, 1)
						require.True(t, req.Config.SkipResizer)
						return []model.File{modelFile()}, nil
					})
				}
				body, contentType := multipartBody(t, fields, field)
				resp, data := send(t, "POST", "/upload", "/upload", f.handler.Upload, body, map[string]string{"Content-Type": contentType})
				require.Equal(t, 202, resp.StatusCode)
				if field == "file" {
					assertQueued(t, responseFile(t, data))
				} else {
					var result struct {
						Files []map[string]any `json:"files"`
					}
					require.NoError(t, json.Unmarshal(data, &result))
					require.Len(t, result.Files, 1)
					assertQueued(t, result.Files[0])
				}
			})
		}
	}
}

func TestUploadBatchPartialResults(t *testing.T) {
	f := newFixture(t)
	f.uploader.EXPECT().UploadBatch(gomock.Any(),
		gomock.Any()).Return([]model.File{modelFile()},
		&uploadservice.BatchError{
			FailedIndex: 1,
			Err:         uploadservice.ClientError{Message: "file is too large"},
		})
	body, ct := multipartBody(t, map[string]string{"entity_type": "post", "entity_id": "55"}, "files")
	resp, data := send(t, "POST", "/upload", "/upload", f.handler.Upload, body, map[string]string{"Content-Type": ct})
	require.Equal(t, 207, resp.StatusCode)
	var result map[string]any
	require.NoError(t, json.Unmarshal(data, &result))
	require.EqualValues(t, 1, result["failedIndex"])
	require.Len(t, result["files"], 1)
}

func TestUploadInputFailures(t *testing.T) {
	for _, test := range []struct {
		name   string
		fields map[string]string
		err    error
		status int
	}{
		{"missing entity", map[string]string{}, nil, 400},
		{"empty batch", map[string]string{"entity_type": "post", "entity_id": "1"}, uploadservice.ErrNoFiles, 400},
		{
			"client failure",
			map[string]string{
				"entity_type": "post",
				"entity_id":   "1",
			},
			uploadservice.ClientError{Message: "denied"},
			400,
		},

		{"internal failure", map[string]string{"entity_type": "post", "entity_id": "1"}, errors.New("database secret"), 500},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			if test.err != nil {
				f.uploader.EXPECT().UploadBatch(gomock.Any(), gomock.Any()).Return(nil, test.err)
			}
			body, ct := multipartBody(t, test.fields, "files")
			resp, data := send(t, "POST", "/", "/", f.handler.Upload, body, map[string]string{"Content-Type": ct})
			require.Equal(t, test.status, resp.StatusCode)
			require.NotContains(t, string(data), "database secret")
		})
	}
}

func TestFileReadContracts(t *testing.T) {
	for _, completed := range []bool{false, true} {
		t.Run(strconv.FormatBool(completed), func(t *testing.T) {
			f := newFixture(t)
			file := modelFile()
			if completed {
				file.Data.Uploader.Status = shared.FileUploadTaskStatusCompleted
				file.FolderPath = "media/v1/post/134/slug"
				file.FileName = "main.png"
				file.URL = ""
			}
			f.uploader.EXPECT().GetFile(gomock.Any(), int64(15)).Return(file, nil)
			resp, data := send(t, "GET", "/files/:id", "/files/15", f.handler.GetFile, nil, nil)
			require.Equal(t, 200, resp.StatusCode)
			got := responseFile(t, data)
			if completed {
				require.Equal(t, "completed", got["status"])
				require.Equal(t, "https://media.example.test/media/v1/post/134/slug/main.png", got["url"])
				require.Equal(t, got["url"], got["publicUrl"])
				require.Equal(t, "media/v1/post/134/slug/main.png", got["fullPath"])
			} else {
				assertQueued(t, got)
			}
		})
	}
	for _, err := range []error{uploadservice.ErrFileNotFound, uploadservice.ErrTaskNotFound} {
		f := newFixture(t)
		f.uploader.EXPECT().GetFile(gomock.Any(), int64(404)).Return(model.File{}, err)
		resp, _ := send(t, "GET", "/files/:id", "/files/404", f.handler.GetFile, nil, nil)
		require.Equal(t, 404, resp.StatusCode)
	}
	f := newFixture(t)
	resp, _ := send(t, "GET", "/files/:id", "/files/bad", f.handler.GetFile, nil, nil)
	require.Equal(t, 400, resp.StatusCode)
}

func TestFileListPolicyAndPagination(t *testing.T) {
	f := newFixture(t)
	file := modelFile()
	file.Data.Uploader.Status = shared.FileUploadTaskStatusCompleted
	file.URL = "https://cdn.example.test/image.png"
	f.repo.EXPECT().List(gomock.Any(),
		gomock.Any()).DoAndReturn(func(_ context.Context,
		filter filerepo.ListFilters,
	) ([]model.File, int, error) {
		require.Equal(t, "image", filter.FileType)
		require.Equal(t, filerepo.MaxListLimit, filter.Limit)
		require.True(t, filter.SkipTotal)
		return []model.File{file}, 1, nil
	})
	resp,
		data := send(t,
		"GET",
		"/files",
		"/files?entity_type=post&entity_id=134&file_type=image&limit=999999",
		f.handler.ListFiles,
		nil,
		nil)
	require.Equal(t, 200, resp.StatusCode)
	var result struct {
		Files    []map[string]any `json:"files"`
		EntityID int64            `json:"entityId"`
	}
	require.NoError(t, json.Unmarshal(data, &result))
	require.Equal(t, int64(134), result.EntityID)
	require.Len(t, result.Files, 1)
	require.EqualValues(t, 2, result.Files[0]["sortOrder"])
	for _, query := range []string{
		"entity_type=post",
		"entity_type=post&entity_id=1&limit=-1",
		"entity_type=post&entity_id=1&offset=-1",
	} {
		resp, _ := send(t, "GET", "/files", "/files?"+query, f.handler.ListFiles, nil, nil)
		require.Equal(t, 400, resp.StatusCode)
	}
}

func TestDeleteTransportContracts(t *testing.T) {
	for _, test := range []struct {
		name, target string
		file         model.File
		serviceErr   error
		status       int
	}{
		{"success", "/files/201?confirm=true", modelFile(), nil, 202},
		{"nil binding", "/files/201?confirm=true", model.File{ID: 201}, nil, 202},
		{"service error", "/files/201?confirm=true", modelFile(), errors.New("private failure"), 500},
		{"not found", "/files/201?confirm=true", model.File{}, nil, 404},
		{"confirmation", "/files/201", model.File{}, nil, 400},
		{"invalid", "/files/bad?confirm=true", model.File{}, nil, 400},
	} {
		t.Run(test.name, func(t *testing.T) {
			f := newFixture(t)
			if test.name != "confirmation" && test.name != "invalid" {
				f.repo.EXPECT().GetByID(gomock.Any(), int64(201)).Return(test.file, nil)
				if test.file.ID > 0 {
					f.uploader.EXPECT().DeleteFile(gomock.Any(),
						gomock.Any()).DoAndReturn(func(_ context.Context,
						req uploadservice.DeleteRequest,
					) error {
						require.Equal(t, int64(201), req.FileID)
						require.Equal(t, f.owner, req.UserRequestID)
						return test.serviceErr
					})
				}
			}
			resp, data := send(t, "DELETE", "/files/:id", test.target, f.handler.DeleteFile, nil, nil)
			require.Equal(t, test.status, resp.StatusCode)
			require.NotContains(t, string(data), "private failure")
		})
	}
}

type imageStrategy struct{}

func (imageStrategy) CanUpload(context.Context, uploadstrategies.UploadContext) error { return nil }
func (imageStrategy) GetConfig(context.Context, uploadstrategies.UploadContext) *uploadservice.FileUploadConfig {
	return &uploadservice.FileUploadConfig{
		MaxFileSize:       10 << 20,
		AllowedExtensions: []string{".png"},
		AllowedMimeTypes:  map[string][]string{".png": {"image/png"}},
	}
}

func (imageStrategy) GetAfterJobs(context.Context, uploadstrategies.UploadContext) ([]shared.FileEventAfterJob, error) {
	return nil, nil
}

type nilConfigStrategy struct{ imageStrategy }

func (nilConfigStrategy) GetConfig(context.Context, uploadstrategies.UploadContext) *uploadservice.FileUploadConfig {
	return nil
}

func tusHeaders() map[string]string {
	return map[string]string{"Tus-Resumable": tusupload.Version, "Content-Type": tusupload.ContentType, "Upload-Offset": "0"}
}

func TestTusTransportLifecycle(t *testing.T) {
	f := newFixture(t)
	meta := metadataHeader(map[string]string{"filename": "test.png", "entity_type": "post", "entity_id": "123"})
	headers := tusHeaders()
	headers["Upload-Length"] = "8"
	headers["Upload-Metadata"] = meta
	resp, _ := send(t, "POST", "/tus", "/tus", f.handler.TusCreate, nil, headers)
	require.Equal(t, 201, resp.StatusCode)
	id := path.Base(resp.Header.Get("Location"))
	require.NotEmpty(t, id)
	resp, _ = send(t, "HEAD", "/tus/:id", "/tus/"+id, f.handler.TusHead, nil, tusHeaders())
	require.Equal(t, 200, resp.StatusCode)
	require.Equal(t, "0", resp.Header.Get("Upload-Offset"))
	require.Equal(t, "8", resp.Header.Get("Upload-Length"))
	require.Equal(t, "no-store", resp.Header.Get("Cache-Control"))
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	resp, _ = send(t, "PATCH", "/tus/:id", "/tus/"+id, f.handler.TusPatch, bytes.NewReader(png), tusHeaders())
	require.Equal(t, 204, resp.StatusCode)
	require.Equal(t, "8", resp.Header.Get("Upload-Offset"))
	f.uploader.EXPECT().UploadReader(gomock.Any(),
		gomock.Any(),
		gomock.Any()).DoAndReturn(func(_ context.Context,
		req uploadservice.ReaderRequest,
		input uploadservice.ReaderUploadInput,
	) (model.File, error) {
		require.Equal(t, id, req.FinalizationKey)
		require.Equal(t, "post", req.ObjectType.String())
		require.EqualValues(t, 123, req.ObjectID)
		require.Equal(t, int64(8), input.Size)
		got, err := io.ReadAll(input.Reader)
		require.NoError(t, err)
		require.Equal(t, png, got)
		return modelFile(), nil
	}).Times(2)
	for range 2 {
		resp, data := send(t, "POST", "/tus/:id/complete", "/tus/"+id+"/complete", f.handler.TusComplete, nil, tusHeaders())
		require.Equal(t, 202, resp.StatusCode)
		assertQueued(t, responseFile(t, data))
	}
	// This transport fixture verifies stable handoff keys, not DB deduplication.
	// The PostgreSQL lifecycle tests prove that both calls resolve one FileID.
	session, err := f.store.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, tusupload.StatusReady, session.Status)
}

func TestCMSTransportRemainsIsolated(t *testing.T) {
	f := newFixture(t)
	headers := tusHeaders()
	headers["Upload-Length"] = "8"
	headers["Upload-Metadata"] = metadataHeader(map[string]string{"filename": "cms.png", "context": "cms", "file_type": "image"})
	resp, _ := send(t, "POST", "/cms/tus", "/cms/tus", f.handler.TusCreateCMS, nil, headers)
	require.Equal(t, 201, resp.StatusCode)
	id := path.Base(resp.Header.Get("Location"))
	png := []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}
	resp, _ = send(t, "PATCH", "/cms/tus/:id", "/cms/tus/"+id, f.handler.TusPatchCMS, bytes.NewReader(png), tusHeaders())
	require.Equal(t, 204, resp.StatusCode)
	session, err := f.store.Get(context.Background(), id)
	require.NoError(t, err)
	require.Equal(t, "cms", session.Metadata["context"])
	require.NotContains(t, session.Metadata, "entity_type")
	require.NotContains(t, session.Metadata, "entity_id")
	f.handler.RegisterStrategy("cms", nilConfigStrategy{})
	resp, _ = send(t, "POST", "/cms/tus", "/cms/tus", f.handler.TusCreateCMS, nil, headers)
	require.Equal(t, 500, resp.StatusCode)
}
