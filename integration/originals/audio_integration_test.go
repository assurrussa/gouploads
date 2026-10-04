//go:build integration

package originals_test

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	gologger "github.com/assurrussa/gologger"
	"github.com/assurrussa/outbox/backends/pgsql/migrations"
	"github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
	"github.com/assurrussa/gouploads/internal/audiofixture"
)

func TestIntegrationAudioOriginals(t *testing.T) {
	for _, driver := range []string{host.StorageDriverLocal, host.StorageDriverS3} {
		t.Run(driver, func(t *testing.T) {
			if driver == host.StorageDriverS3 && os.Getenv("TEST_S3_ENDPOINT") == "" {
				t.Skip("TEST_S3_ENDPOINT is required for MinIO acceptance")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			database, _, cleanup := hosttest.PrepareDB(ctx, t, "audio_originals")
			defer cleanup(context.Background())
			sqlDB := stdlib.OpenDBFromPool(database.DB().(storage.DBPgxEnginePool).Pool())
			provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithTableName("outbox_schema_versions"))
			require.NoError(t, err)
			_, err = provider.Up(ctx)
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
			queue := newQueue(t, database)
			runtime, err := host.NewOriginalRuntime(storageConfig(t, ctx, driver), host.OriginalRuntimeDeps{
				Database: database, Transaction: transaction.New(database.DB()), Outbox: queue,
			})
			require.NoError(t, err)
			for _, job := range runtime.Jobs {
				require.NotEqual(t, "send_resize_file", job.Name())
			}
			user := host.NewUserID()
			for _, sample := range []struct {
				name, mime string
				body       []byte
			}{
				{"tagged.mp3", "audio/mpeg", audiofixture.MP3},
				{"untagged.mp3", "audio/mpeg", audiofixture.UntaggedMP3()},
				{"tone.wav", "audio/wav", audiofixture.WAV},
			} {
				t.Run(sample.name, func(t *testing.T) {
					first, err := runtime.Uploader.UploadReader(ctx, host.ReaderRequest{
						UploaderUUID: user,
						UserID:       101, ObjectType: host.ObjectTypeAdmin, ObjectID: 42, Config: audioConfig(),
					}, host.ReaderUploadInput{
						OriginalName: sample.name, Size: int64(len(sample.body)), Reader: bytes.NewReader(sample.body),
					})
					require.NoError(t, err)
					tus, app := tusAudioUpload(t, ctx, runtime, user, sample.name, sample.body)
					worker := newQueue(t, database)
					require.NoError(t, worker.RegisterJobs(runtime.Jobs...))
					stop := startWorker(t, ctx, worker)
					defer stop()
					waitEmpty(t, ctx, worker)
					for _, queued := range []host.File{first, tus} {
						verifyComplete(t, ctx, runtime, queued, sample.body)
						file, err := runtime.Files.GetByID(ctx, queued.ID)
						require.NoError(t, err)
						require.Equal(t, host.FileTypeAudio, file.FileType)
						require.Equal(t, sample.mime, file.MimeType)
						require.Equal(t, sample.mime, file.Data.Presets["main"].MimeType)
						response, body := fiberRequest(t, ctx, app, http.MethodGet, fmt.Sprintf("/files/%d", file.ID), nil, nil)
						require.Equal(t, http.StatusOK, response.StatusCode, string(body))
						var decoded struct {
							File struct {
								FileType string `json:"fileType"`
								MimeType string `json:"mimeType"`
								Size     int64  `json:"size"`
							} `json:"file"`
						}
						require.NoError(t, json.Unmarshal(body, &decoded))
						require.Equal(t, "7", decoded.File.FileType)
						require.Equal(t, sample.mime, decoded.File.MimeType)
						require.EqualValues(t, len(sample.body), decoded.File.Size)
					}
					_, err = queue.Put(ctx, originalJob, fmt.Sprintf(`{"fileId":%d}`, first.ID), time.Now())
					require.NoError(t, err)
					waitEmpty(t, ctx, worker)
					verifyComplete(t, ctx, runtime, first, sample.body)
				})
			}
			for _, filter := range []string{"audio", "7", "audio/"} {
				files, total, err := runtime.Files.List(ctx, host.ListFilters{FileType: filter})
				require.NoError(t, err)
				require.Len(t, files, 6)
				require.Equal(t, 6, total)
			}
		})
	}
}

func audioConfig() *host.FileUploadConfig {
	return &host.FileUploadConfig{
		MaxFileSize: 50 << 20, AllowedExtensions: []string{".mp3", ".wav"},
		AllowedMimeTypes: map[string][]string{".mp3": {"audio/mpeg"}, ".wav": {"audio/x-wav"}},
	}
}

type audioStrategy struct{}

func (audioStrategy) CanUpload(context.Context, host.UploadContext) error { return nil }
func (audioStrategy) GetConfig(context.Context, host.UploadContext) *host.FileUploadConfig {
	return audioConfig()
}

func (audioStrategy) GetAfterJobs(context.Context, host.UploadContext) ([]host.FileEventAfterJob, error) {
	return nil, nil
}

func tusAudioUpload(t *testing.T, ctx context.Context, runtime *host.OriginalRuntime, user host.UserID, name string, body []byte) (host.File, *fiber.App) {
	t.Helper()
	handler := host.NewUploadHandler(runtime.Uploader, runtime.Files, runtime.TusStore, gologger.Discard(),
		func(_ context.Context, metadata map[string]string) (host.UploadContext, error) {
			return host.UploadContext{UserID: 101, UserUUID: user, Metadata: metadata}, nil
		}, nil)
	handler.RegisterStrategy("audio-fixture", audioStrategy{})
	app := fiber.New()
	host.NewFiberUploadHandler(handler).RegisterGroupRoutes("/files", app)
	metadata := map[string]string{"filename": name, "entity_type": "admin", "entity_id": "42", "file_type": "audio", "context": "audio-fixture"}
	parts := make([]string, 0, len(metadata))
	for key, value := range metadata {
		parts = append(parts, key+" "+base64.StdEncoding.EncodeToString([]byte(value)))
	}
	response, result := fiberRequest(t, ctx, app, http.MethodPost, "/files/tus", nil, map[string]string{
		"Tus-Resumable": "1.0.0", "Upload-Length": strconv.Itoa(len(body)), "Upload-Metadata": strings.Join(parts, ","),
	})
	require.Equal(t, http.StatusCreated, response.StatusCode, string(result))
	location := response.Header.Get("Location")
	// Local TUS accepts tiny prefixes; S3 preserves its fixed multipart chunk profile.
	prefix := 0
	if handler.TusChunkSize() == 0 {
		prefix = 12
	}
	for offset := 0; offset < prefix; offset++ {
		response, result = fiberRequest(t, ctx, app, http.MethodPatch, location, body[offset:offset+1], map[string]string{
			"Tus-Resumable": "1.0.0", "Content-Type": "application/offset+octet-stream", "Upload-Offset": strconv.Itoa(offset),
		})
		require.Equal(t, http.StatusNoContent, response.StatusCode, string(result))
	}
	response, result = fiberRequest(t, ctx, app, http.MethodPatch, location, body[prefix:], map[string]string{
		"Tus-Resumable": "1.0.0", "Content-Type": "application/offset+octet-stream", "Upload-Offset": strconv.Itoa(prefix),
	})
	require.Equal(t, http.StatusNoContent, response.StatusCode, string(result))
	response, result = fiberRequest(t, ctx, app, http.MethodPost, location+"/complete", nil, map[string]string{"Tus-Resumable": "1.0.0"})
	require.Equal(t, http.StatusAccepted, response.StatusCode, string(result))
	var decoded struct {
		File struct {
			ID int64 `json:"id"`
		} `json:"file"`
	}
	require.NoError(t, json.Unmarshal(result, &decoded))
	file, err := runtime.Files.GetByID(ctx, decoded.File.ID)
	require.NoError(t, err)
	return file, app
}
