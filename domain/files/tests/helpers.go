package testshelpers

import (
	"bytes"
	"context"
	"database/sql"
	"image"
	"image/color"
	"image/png"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"path"
	"strings"
	"testing"
	"time"

	commonshared "github.com/assurrussa/goshared/pkg/filetypes"
	"github.com/assurrussa/goshared/pkg/pointer"
	"github.com/assurrussa/goshared/pkg/sharedtypes"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	"github.com/assurrussa/gouploads/domain/files/shared"
)

func MakeFileHeader(t *testing.T, fieldName, fileName, content, contentType string) *multipart.FileHeader {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(fieldName, fileName)
	require.NoError(t, err)
	_, err = io.Copy(part, strings.NewReader(content))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", body)
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

func MakeFileHeaderImage(t *testing.T, fieldName, fileName, contentType string) *multipart.FileHeader {
	t.Helper()

	body := &bytes.Buffer{}
	writer := multipart.NewWriter(body)
	part, err := writer.CreateFormFile(fieldName, fileName)
	require.NoError(t, err)
	_, err = io.Copy(part, CreateTestImage(t))
	require.NoError(t, err)
	require.NoError(t, writer.Close())

	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/", body)
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

func CreateTestImage(t *testing.T) io.Reader {
	t.Helper()

	img := image.NewRGBA(image.Rect(0, 0, 2, 2))
	img.Set(0, 0, color.RGBA{R: 255, G: 0, B: 0, A: 255})
	img.Set(1, 1, color.RGBA{R: 0, G: 255, B: 0, A: 255})

	var buf bytes.Buffer
	require.NoError(t, png.Encode(&buf, img))

	return bytes.NewReader(buf.Bytes())
}

func CreateFile(t *testing.T) model.File {
	t.Helper()

	objectType := shared.ObjectTypeAdmin
	objectID := shared.FileObjectID(12)
	uploadPath := []string{"uploads", objectType.String(), objectID.String()}
	folderPath := path.Join(uploadPath...)
	fileName := "example.png"
	fileURL := "https://s3store.localhost/" + path.Join(folderPath, fileName)
	tmNow := time.Now()
	userID := sharedtypes.MustParse[sharedtypes.UserID]("ddfe05b2-847d-4e35-8249-1cf778b90bd5")

	return model.File{
		ID:               123456,
		UserID:           pointer.To[int64](123),
		ManagerID:        pointer.To[int64](24),
		ObjectType:       objectType,
		ObjectID:         pointer.To(objectID),
		OriginalFileName: "example_origin.png",
		FileName:         fileName,
		FolderPath:       folderPath,
		Size:             12345,
		MimeType:         "image/png",
		FileType:         commonshared.FileTypeImage,
		URL:              fileURL,
		Slug:             uuid.NewString(),
		Data: &model.FileData{
			Width:  2,
			Height: 2,
			Uploader: shared.FileUploader{
				UserID:   123,
				UserUUID: userID,
				Status:   shared.FileUploadTaskStatusProcessing,
				Type:     shared.UserTypeAdmin,
				AfterJobs: shared.NewFileEventAfterJobs("test_name_job", userID, map[string]any{
					"foo": "bar",
				}),
			},
		},
		IsPrimary: false,
		CreatedAt: tmNow,
		UpdatedAt: tmNow,
		PublishedAt: sql.NullTime{
			Valid: true,
			Time:  tmNow,
		},
	}
}
