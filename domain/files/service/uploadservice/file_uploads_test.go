package uploadservice_test

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/assurrussa/goshared/pkg/logger"
	"github.com/assurrussa/goshared/pkg/tests"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/stretchr/testify/suite"
	"go.uber.org/mock/gomock"

	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	uploadservicemocks "github.com/assurrussa/gouploads/domain/files/service/uploadservice/mocks"
	localfilestorage "github.com/assurrussa/gouploads/infrastructure/storage/files/local"
)

type TestFileSuite struct {
	suite.Suite

	ctrl               *gomock.Controller
	mockFileRepository *uploadservicemocks.MockfileRepository
	mockOutboxPutter   *uploadservicemocks.MockoutboxPutter
	mockTransactor     *uploadservicemocks.Mocktransactor
	storage            *localfilestorage.Storage

	svc *uploadservice.Service
}

func NewTestFileSuite(t *testing.T) (context.Context, context.CancelFunc, *TestFileSuite) {
	t.Helper()
	return tests.NewSuite[*TestFileSuite](t, func(t *testing.T, _ context.Context) *TestFileSuite {
		t.Helper()

		ctrl := gomock.NewController(t)
		mockFileRepository := uploadservicemocks.NewMockfileRepository(ctrl)
		mockOutboxPutter := uploadservicemocks.NewMockoutboxPutter(ctrl)
		mockTransactor := uploadservicemocks.NewMocktransactor(ctrl)
		storage, err := localfilestorage.New(t.TempDir(), "")
		require.NoError(t, err)

		svc := uploadservice.Must(uploadservice.NewOptions(
			mockTransactor,
			mockOutboxPutter,
			mockFileRepository,
			logger.Discard(),
			storage,
			"https://ceph.localhost",
		))

		return &TestFileSuite{
			ctrl:               ctrl,
			mockFileRepository: mockFileRepository,
			mockOutboxPutter:   mockOutboxPutter,
			mockTransactor:     mockTransactor,
			storage:            storage,
			svc:                svc,
		}
	})
}

func TestDefaultFileUploadConfig(t *testing.T) {
	config := uploadservice.DefaultFileUploadConfig()

	assert.Equal(t, int64(10*1024*1024), config.MaxFileSize)
	assert.Contains(t, config.AllowedExtensions, ".jpg")
	assert.Contains(t, config.AllowedExtensions, ".png")
	assert.Contains(t, config.AllowedExtensions, ".pdf")
	assert.Equal(t, "upload_file", config.UploadDir)
	assert.NotEmpty(t, config.AllowedMimeTypes[".jpg"])
}

func TestProcessSingleFileUpload_Success(t *testing.T) {
	ctx, _, ts := NewTestFileSuite(t)

	config := uploadservice.DefaultFileUploadConfig()
	config.UploadDir = "upload_file/avatars/2025"
	config.AllowedExtensions = []string{".txt"}
	config.AllowedMimeTypes = map[string][]string{
		".txt": {"text/plain"},
	}

	header := newFileHeader(t, "file", "avatar.txt", "avatar content", "text/plain")

	file, err := ts.svc.ProcessSingleFileUpload(ctx, header, config)
	require.NoError(t, err)
	require.NotEmpty(t, file.FileName)
	assert.True(t, strings.HasPrefix(file.MimeType, "text/plain"))
	assert.Equal(t, int64(len("avatar content")), file.Size)
	assert.Equal(t, filepath.Join("tmp", "uploads", "upload_file", "avatars", "2025"), file.FolderPath)
	assert.True(t, strings.HasPrefix(file.URL, "/"))

	stored, err := ts.storage.Open(ctx, file.Path)
	require.NoError(t, err)
	data, err := io.ReadAll(stored)
	_ = stored.Close()
	require.NoError(t, err)
	assert.Equal(t, "avatar content", string(data))
}

func TestProcessSingleFileUpload_Rich_Success(t *testing.T) {
	ctx, _, ts := NewTestFileSuite(t)

	config := uploadservice.DefaultFileUploadRichTextConfig()
	config.UploadDir = "upload_file/rich/avatars/2025"
	config.AllowedExtensions = []string{".txt"}
	config.AllowedMimeTypes = map[string][]string{
		".txt": {"text/plain"},
	}

	header := newFileHeader(t, "file", "avatar.txt", "avatar content", "text/plain")

	file, err := ts.svc.ProcessSingleFileUpload(ctx, header, config)
	require.NoError(t, err)
	require.NotEmpty(t, file.FileName)
	assert.True(t, strings.HasPrefix(file.MimeType, "text/plain"))
	assert.Equal(t, filepath.Join("tmp", "uploads", "upload_file", "rich", "avatars", "2025"), file.FolderPath)
	assert.True(t, strings.HasPrefix(file.URL, "/"))

	stored, err := ts.storage.Open(ctx, file.Path)
	require.NoError(t, err)
	data, err := io.ReadAll(stored)
	_ = stored.Close()
	require.NoError(t, err)
	assert.Equal(t, "avatar content", string(data))
}

func TestProcessSingleFileUpload_SizeGuardAgainstStream(t *testing.T) {
	ctx, _, ts := NewTestFileSuite(t)

	config := &uploadservice.FileUploadConfig{
		MaxFileSize:       5,
		AllowedExtensions: []string{".txt"},
		AllowedMimeTypes: map[string][]string{
			".txt": {"text/plain"},
		},
		UploadDir: "upload_file/guard",
	}

	oversizedContent := strings.Repeat("a", 10)
	header := newFileHeader(t, "file", "oversize.txt", oversizedContent, "text/plain")
	header.Size = 1 // simulate incorrect size metadata from client

	_, err := ts.svc.ProcessSingleFileUpload(ctx, header, config)
	require.Error(t, err)
	assert.Equal(t, "Превышен допустимый размер файла", err.Error())
}

func TestProcessSingleFileUpload_InvalidExtension(t *testing.T) {
	ctx, _, ts := NewTestFileSuite(t)

	config := uploadservice.DefaultFileUploadConfig()
	config.AllowedExtensions = []string{".png"}

	header := newFileHeader(t, "file", "avatar.txt", "avatar content", "text/plain")

	_, err := ts.svc.ProcessSingleFileUpload(ctx, header, config)
	require.Error(t, err)
	assert.Equal(t, "Недопустимое расширение файла", err.Error())
}

func TestProcessSingleFileUpload_NilHeader(t *testing.T) {
	ctx, _, ts := NewTestFileSuite(t)

	config := &uploadservice.FileUploadConfig{UploadDir: "tmp/upload_file"}

	_, err := ts.svc.ProcessSingleFileUpload(ctx, nil, config)
	require.Error(t, err)
	assert.Equal(t, "Не найден файл для обработки", err.Error())

	var coded codedError
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, "file_header_missing", coded.Code())
}

func TestProcessSingleFileUpload_SaveTempError(t *testing.T) {
	ctx, _, ts := NewTestFileSuite(t)

	config := uploadservice.DefaultFileUploadConfig()
	config.UploadDir = "../outside"
	config.AllowedExtensions = []string{".txt"}
	config.AllowedMimeTypes = map[string][]string{
		".txt": {"text/plain"},
	}

	header := newFileHeader(t, "file", "note.txt", "content", "text/plain")

	_, err := ts.svc.ProcessSingleFileUpload(ctx, header, config)
	require.Error(t, err)
	assert.Equal(t, "Не удалось сохранить файл", err.Error())

	wrapped := errors.Unwrap(err)
	require.Error(t, wrapped)
	assert.Contains(t, wrapped.Error(), "path escapes root")

	var coded codedError
	require.ErrorAs(t, err, &coded)
	assert.Equal(t, "save_temp_failed", coded.Code())
}

type codedError interface {
	Code() string
}

//nolint:unparam // tests
func newFileHeader(t *testing.T, fieldName, fileName, content, contentType string) *multipart.FileHeader {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(fieldName, fileName)
	require.NoError(t, err)
	_, err = io.Copy(part, strings.NewReader(content))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequest(http.MethodPost, "/", body)
	req.Header.Set("Content-Type", writer.FormDataContentType())
	require.NoError(t, req.ParseMultipartForm(int64(body.Len())))

	fileHeaders := req.MultipartForm.File[fieldName]
	require.Len(t, fileHeaders, 1)

	fh := fileHeaders[0]
	if contentType != "" {
		fh.Header.Set("Content-Type", contentType)
	}

	return fh
}
