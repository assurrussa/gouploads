//go:build integration

package originals_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
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

	gologger "github.com/assurrussa/gologger"
	"github.com/assurrussa/outbox/backends/pgsql/migrations"
	"github.com/assurrussa/outbox/backends/pgsql/repositories/jobsfailedrepo"
	"github.com/assurrussa/outbox/backends/pgsql/repositories/jobsrepo"
	"github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/assurrussa/outbox/outbox"
	"github.com/assurrussa/outbox/outbox/logger"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	smithyhttp "github.com/aws/smithy-go/transport/http"
	"github.com/gofiber/fiber/v3"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
)

const originalJob = "finalize_original_file"

// Both drivers use a real PostgreSQL queue; no media service or callback exists.
func TestIntegrationOriginals(t *testing.T) {
	for _, driver := range []string{host.StorageDriverLocal, host.StorageDriverS3} {
		t.Run(driver, func(t *testing.T) {
			if driver == host.StorageDriverS3 && os.Getenv("TEST_S3_ENDPOINT") == "" {
				t.Skip("TEST_S3_ENDPOINT is required for MinIO acceptance")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
			defer cancel()
			database, _, cleanup := hosttest.PrepareDB(ctx, t, "originals")
			defer cleanup(context.Background())
			sqlDB := stdlib.OpenDBFromPool(database.DB().(storage.DBPgxEnginePool).Pool())
			provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithTableName("outbox_schema_versions"))
			require.NoError(t, err)
			_, err = provider.Up(ctx)
			require.NoError(t, err)
			require.NoError(t, sqlDB.Close())
			cfg := storageConfig(t, ctx, driver)
			producer := newQueue(t, database)
			runtime, err := host.NewOriginalRuntime(cfg, host.OriginalRuntimeDeps{Database: database, Transaction: transaction.New(database.DB()), Outbox: producer})
			require.NoError(t, err)
			user := host.NewUserID()
			body := []byte("%PDF-1.7\nstandalone original integration payload\n%%EOF\n")
			request := host.ReaderRequest{UploaderUUID: user, UserID: 101, ObjectType: host.ObjectTypeAdmin, ObjectID: 42, Config: uploadConfig()}
			first, err := runtime.Uploader.UploadReader(ctx, request, host.ReaderUploadInput{OriginalName: "reader.pdf", Size: int64(len(body)), Reader: bytes.NewReader(body)})
			require.NoError(t, err)
			single, err := runtime.Uploader.UploadSingle(ctx, host.SingleRequest{UploaderUUID: user, UserID: 101, ObjectType: host.ObjectTypeAdmin, ObjectID: 42, Config: uploadConfig(), FileHeader: multipartHeaders(t, body, 1)[0]})
			require.NoError(t, err)
			batch, err := runtime.Uploader.UploadBatch(ctx, host.BatchRequest{UploaderUUID: user, UserID: 101, ObjectType: host.ObjectTypeAdmin, ObjectID: 42, Config: uploadConfig(), FileHeaders: multipartHeaders(t, body, 2)})
			require.NoError(t, err)
			require.Len(t, batch, 2)
			tusFile := tusUpload(t, ctx, runtime, user, body)
			files := append([]host.File{first, single, tusFile}, batch...)
			for _, file := range files {
				queued, readErr := runtime.Files.GetByID(ctx, file.ID)
				require.NoError(t, readErr)
				require.Equal(t, host.FileUploadTaskStatusQueued, queued.GetData().Uploader.Status)
				requireBytes(t, ctx, runtime.Storage, queued.GetFullPath(), body)
			}
			stats, err := producer.GetQueueStats(ctx)
			require.NoError(t, err)
			require.EqualValues(t, len(files), stats.Total)
			concurrentDuplicates(t, ctx, database, runtime, first)
			// A fresh service, rebuilt runtime and fresh handlers consume durable rows.
			// No producer-side collector or direct Handle invocation completes this phase.
			worker := newQueue(t, database)
			fresh, err := host.NewOriginalRuntime(cfg, host.OriginalRuntimeDeps{Database: database, Transaction: transaction.New(database.DB()), Outbox: worker})
			require.NoError(t, err)
			require.NoError(t, worker.RegisterJobs(fresh.Jobs...))
			stop := startWorker(t, ctx, worker)
			waitEmpty(t, ctx, worker)
			for _, file := range files {
				verifyComplete(t, ctx, fresh, file, body)
			}
			// Independent duplicate queue rows exercise repeated deliveries after cleanup.
			for range 2 {
				_, err = producer.Put(ctx, originalJob, fmt.Sprintf(`{"fileId":%d}`, first.ID), time.Now())
				require.NoError(t, err)
			}
			waitEmpty(t, ctx, worker)
			verifyComplete(t, ctx, fresh, first, body)
			foreign := request
			foreign.ObjectID = 43
			foreign.DeletedID = host.ObjectID(first.ID)
			_, err = fresh.Uploader.UploadReader(ctx, foreign, host.ReaderUploadInput{OriginalName: "foreign.pdf", Size: int64(len(body)), Reader: bytes.NewReader(body)})
			require.Error(t, err)
			verifyComplete(t, ctx, fresh, first, body)
			replacement := request
			replacement.DeletedID = host.ObjectID(first.ID)
			finalFirst, err := fresh.Files.GetByID(ctx, first.ID)
			require.NoError(t, err)
			next, err := fresh.Uploader.UploadReader(ctx, replacement, host.ReaderUploadInput{OriginalName: "replacement.pdf", Size: int64(len(body)), Reader: bytes.NewReader(body)})
			require.NoError(t, err)
			waitEmpty(t, ctx, worker)
			verifyComplete(t, ctx, fresh, next, body)
			deleted, err := fresh.Files.GetByID(ctx, first.ID)
			require.NoError(t, err)
			require.Zero(t, deleted.ID)
			exists, err := fresh.Storage.Exists(ctx, host.ExistFileInput{Path: finalFirst.GetFullPath()})
			require.NoError(t, err)
			require.False(t, exists.Exist)
			require.NoError(t, fresh.Uploader.DeleteFile(ctx, host.DeleteRequest{UserRequestID: user, FileID: next.ID}))
			waitEmpty(t, ctx, worker)
			deleted, err = fresh.Files.GetByID(ctx, next.ID)
			require.NoError(t, err)
			require.Zero(t, deleted.ID)
			stop()

			racing, err := fresh.Uploader.UploadReader(ctx, request, host.ReaderUploadInput{OriginalName: "racing.pdf", Size: int64(len(body)), Reader: bytes.NewReader(body)})
			require.NoError(t, err)
			concurrentFinalizeDelete(t, ctx, database, fresh, racing, user)
			restart := newQueue(t, database)
			restarted, err := host.NewOriginalRuntime(cfg, host.OriginalRuntimeDeps{Database: database, Transaction: transaction.New(database.DB()), Outbox: restart})
			require.NoError(t, err)
			require.NoError(t, restart.RegisterJobs(restarted.Jobs...))
			stopRestart := startWorker(t, ctx, restart)
			waitEmpty(t, ctx, restart)
			stopRestart()
			failed, err := jobsfailedrepo.Must(jobsfailedrepo.NewOptions(database)).CountExact(ctx)
			require.NoError(t, err)
			require.Zero(t, failed, "no acceptance job may land in the dead-letter queue")
			for _, key := range []string{racing.GetFullPath(), path.Join("media/v1", "admin", "42", racing.Slug, "main.pdf")} {
				if driver == host.StorageDriverLocal && strings.HasPrefix(key, "media/v1/") {
					key = path.Join("uploads", key)
				}
				exists, checkErr := fresh.Storage.Exists(ctx, host.ExistFileInput{Path: key})
				require.NoError(t, checkErr)
				require.False(t, exists.Exist, "racing deletion must remove %s", key)
			}
		})
	}
}

func newQueue(t *testing.T, db *hosttest.PgsqlClient) *outbox.Service {
	t.Helper()
	jobs := jobsrepo.Must(jobsrepo.NewOptions(db))
	failed := jobsfailedrepo.Must(jobsfailedrepo.NewOptions(db))
	queue, err := outbox.New(outbox.WithJobsRepo(jobs), outbox.WithJobsStatRepo(jobs), outbox.WithJobsFailedRepo(failed), outbox.WithTransactor(transaction.New(db.DB())), outbox.WithWorkers(2), outbox.WithIdleTime(100*time.Millisecond), outbox.WithReserveFor(time.Minute), outbox.WithLogger(logger.Discard()))
	require.NoError(t, err)
	return queue
}

func startWorker(t *testing.T, ctx context.Context, worker *outbox.Service) func() {
	t.Helper()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- worker.Run(runCtx) }()
	stopped := false
	stop := func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Fatal("outbox worker failed to stop")
		}
	}
	t.Cleanup(stop)
	return stop
}

func waitEmpty(t *testing.T, ctx context.Context, worker *outbox.Service) {
	t.Helper()
	require.Eventually(t, func() bool { stats, err := worker.GetQueueStats(ctx); return err == nil && stats.Total == 0 }, 20*time.Second, 10*time.Millisecond, "durable outbox must finish originals and cleanup")
}

func verifyComplete(t *testing.T, ctx context.Context, runtime *host.OriginalRuntime, queued host.File, body []byte) {
	t.Helper()
	file, err := runtime.Files.GetByID(ctx, queued.ID)
	require.NoError(t, err)
	require.Positive(t, file.ID)
	require.Empty(t, file.GetData().Uploader)
	require.Empty(t, file.URL)
	require.Len(t, file.GetData().Presets, 1)
	preset, found := file.GetData().Presets[host.PresetName("main")]
	require.True(t, found)
	sum := sha256.Sum256(body)
	require.Equal(t, hex.EncodeToString(sum[:]), preset.ChecksumSHA256)
	require.Equal(t, preset.RelativePath, file.GetFullPath())
	require.EqualValues(t, len(body), file.Size)
	requireBytes(t, ctx, runtime.Storage, file.GetFullPath(), body)
	exists, err := runtime.Storage.Exists(ctx, host.ExistFileInput{Path: queued.GetFullPath()})
	require.NoError(t, err)
	require.False(t, exists.Exist, "staging source must be cleaned")
}

func requireBytes(t *testing.T, ctx context.Context, store host.Storage, key string, want []byte) {
	t.Helper()
	reader, err := store.Open(ctx, key)
	require.NoError(t, err)
	body, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, want, body)
}

func uploadConfig() *host.FileUploadConfig {
	return &host.FileUploadConfig{UploadDir: "tmp/uploads", MaxFileSize: 1024 * 1024, AllowedExtensions: []string{".pdf"}, AllowedMimeTypes: map[string][]string{".pdf": {"application/pdf"}}}
}

func multipartHeaders(t *testing.T, body []byte, count int) []*multipart.FileHeader {
	t.Helper()
	var buffer bytes.Buffer
	writer := multipart.NewWriter(&buffer)
	for index := range count {
		part, err := writer.CreateFormFile("files", fmt.Sprintf("single-%d.pdf", index))
		require.NoError(t, err)
		_, err = part.Write(body)
		require.NoError(t, err)
	}
	require.NoError(t, writer.Close())
	request := httptest.NewRequest(http.MethodPost, "/upload", &buffer)
	request.Header.Set("Content-Type", writer.FormDataContentType())
	require.NoError(t, request.ParseMultipartForm(1024*1024))
	t.Cleanup(func() { require.NoError(t, request.MultipartForm.RemoveAll()) })
	return request.MultipartForm.File["files"]
}

func tusUpload(t *testing.T, ctx context.Context, runtime *host.OriginalRuntime, user host.UserID, body []byte) host.File {
	t.Helper()
	handler := host.NewUploadHandler(runtime.Uploader, runtime.Files, runtime.TusStore, gologger.Discard(), func(_ context.Context, metadata map[string]string) (host.UploadContext, error) {
		return host.UploadContext{UserID: 101, UserUUID: user, Metadata: metadata}, nil
	}, func(key string) string { return "https://files.test/" + key })
	app := fiber.New()
	host.NewFiberUploadHandler(handler).RegisterGroupRoutes("/files/", app)
	metadata := map[string]string{"filename": "tus.pdf", "entity_type": "admin", "entity_id": "42", "file_type": "pdf"}
	parts := make([]string, 0, len(metadata))
	for key, value := range metadata {
		parts = append(parts, key+" "+base64.StdEncoding.EncodeToString([]byte(value)))
	}
	response, _ := fiberRequest(t, ctx, app, http.MethodPost, "/files/tus", nil, map[string]string{"Tus-Resumable": "1.0.0", "Upload-Length": strconv.Itoa(len(body)), "Upload-Metadata": strings.Join(parts, ",")})
	require.Equal(t, http.StatusCreated, response.StatusCode)
	location := response.Header.Get("Location")
	require.NotEmpty(t, location)
	response, _ = fiberRequest(t, ctx, app, http.MethodPatch, location, body, map[string]string{"Tus-Resumable": "1.0.0", "Content-Type": "application/offset+octet-stream", "Upload-Offset": "0"})
	require.Equal(t, http.StatusNoContent, response.StatusCode)
	response, result := fiberRequest(t, ctx, app, http.MethodPost, location+"/complete", nil, map[string]string{"Tus-Resumable": "1.0.0"})
	require.Equal(t, http.StatusAccepted, response.StatusCode, string(result))
	var decoded struct {
		File struct {
			ID int64 `json:"id"`
		} `json:"file"`
	}
	require.NoError(t, json.Unmarshal(result, &decoded))
	file, err := runtime.Files.GetByID(ctx, decoded.File.ID)
	require.NoError(t, err)
	require.Positive(t, file.ID)
	return file
}

func fiberRequest(t *testing.T, ctx context.Context, app *fiber.App, method, url string, body []byte, headers map[string]string) (*http.Response, []byte) {
	t.Helper()
	request := httptest.NewRequestWithContext(ctx, method, url, bytes.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response, err := app.Test(request)
	require.NoError(t, err)
	result, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())
	return response, result
}

func storageConfig(t *testing.T, ctx context.Context, driver string) host.StorageConfig {
	t.Helper()
	cfg := host.StorageConfig{Driver: driver, Public: host.StoragePublicConfig{Prefix: "media/v1"}, Tus: host.StorageTusConfig{SessionTTL: time.Hour, CleanupInterval: time.Hour}}
	if driver == host.StorageDriverLocal {
		cfg.Local = host.StorageLocalConfig{Root: t.TempDir(), BaseURL: "https://files.test"}
		return cfg
	}
	endpoint := os.Getenv("TEST_S3_ENDPOINT")
	access := envOr("TEST_S3_ACCESS_KEY", "minioadmin")
	secret := envOr("TEST_S3_SECRET_KEY", "minioadmin")
	client := s3.NewFromConfig(aws.Config{Region: "us-east-1", Credentials: credentials.NewStaticCredentialsProvider(access, secret, "")}, func(options *s3.Options) { options.BaseEndpoint = aws.String(endpoint); options.UsePathStyle = true })
	bucket := "originals-" + strings.ReplaceAll(host.NewUserID().String(), "-", "")
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
	t.Cleanup(func() {
		cleanupCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		defer cancel()
		listed, err := client.ListObjectsV2(cleanupCtx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
		if err == nil && len(listed.Contents) > 0 {
			objects := make([]s3types.ObjectIdentifier, 0, len(listed.Contents))
			for _, object := range listed.Contents {
				objects = append(objects, s3types.ObjectIdentifier{Key: object.Key})
			}
			_, err = client.DeleteObjects(cleanupCtx, &s3.DeleteObjectsInput{
				Bucket: aws.String(bucket), Delete: &s3types.Delete{Objects: objects},
			}, func(options *s3.Options) {
				options.APIOptions = append(options.APIOptions, smithyhttp.AddContentChecksumMiddleware)
			})
		}
		require.NoError(t, err)
		_, err = client.DeleteBucket(cleanupCtx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)})
		require.NoError(t, err)
	})
	cfg.Public.BaseURL = endpoint + "/" + bucket
	cfg.S3 = host.StorageS3Config{Endpoint: endpoint, Region: "us-east-1", Bucket: bucket, StagingBucket: bucket, AccessKey: access, SecretKey: secret, ForcePathStyle: true, Timeout: 5 * time.Second, MaxRetries: 1}
	return cfg
}

func envOr(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return value
	}
	return fallback
}

// Holding the real row lock and observing PostgreSQL lock waiters proves both
// handlers reached the contested persistence operation before it is released.
func concurrentDuplicates(t *testing.T, ctx context.Context, db *hosttest.PgsqlClient, runtime *host.OriginalRuntime, file host.File) {
	t.Helper()
	tx, err := db.DB().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, "SELECT id FROM files WHERE id=$1 FOR UPDATE", file.ID)
	require.NoError(t, err)
	job := findJob(t, runtime.Jobs, originalJob)
	done := make(chan error, 2)
	for range 2 {
		go func() { done <- job.Handle(ctx, fmt.Sprintf(`{"fileId":%d}`, file.ID)) }()
	}
	waitLocks(t, ctx, db, 2)
	require.NoError(t, tx.Commit(ctx))
	for range 2 {
		select {
		case err = <-done:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
}

func concurrentFinalizeDelete(t *testing.T, ctx context.Context, db *hosttest.PgsqlClient, runtime *host.OriginalRuntime, file host.File, user host.UserID) {
	t.Helper()
	tx, err := db.DB().BeginTx(ctx, pgx.TxOptions{})
	require.NoError(t, err)
	defer func() { _ = tx.Rollback(context.Background()) }()
	_, err = tx.Exec(ctx, "SELECT id FROM files WHERE id=$1 FOR UPDATE", file.ID)
	require.NoError(t, err)
	finalize := findJob(t, runtime.Jobs, originalJob)
	deleteJob := findJob(t, runtime.Jobs, "deleted_file")
	done := make(chan error, 2)
	go func() { done <- finalize.Handle(ctx, fmt.Sprintf(`{"fileId":%d}`, file.ID)) }()
	waitLocks(t, ctx, db, 1)
	payload, err := json.Marshal(map[string]any{"fileId": file.ID, "userId": user, "filepath": file.GetFullPath()})
	require.NoError(t, err)
	go func() { done <- deleteJob.Handle(ctx, string(payload)) }()
	waitLocks(t, ctx, db, 2)
	require.NoError(t, tx.Commit(ctx))
	for range 2 {
		select {
		case err = <-done:
			require.NoError(t, err)
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		}
	}
	deleted, err := runtime.Files.GetByID(ctx, file.ID)
	require.NoError(t, err)
	require.Zero(t, deleted.ID)
	// A delivery arriving after deletion must never recreate the row or object.
	require.NoError(t, finalize.Handle(ctx, fmt.Sprintf(`{"fileId":%d}`, file.ID)))
}

func findJob(t *testing.T, jobs []outbox.Job, name string) outbox.Job {
	t.Helper()
	for _, job := range jobs {
		if job.Name() == name {
			return job
		}
	}
	t.Fatalf("job %s not registered", name)
	return nil
}

func waitLocks(t *testing.T, ctx context.Context, db *hosttest.PgsqlClient, want int) {
	t.Helper()
	pool := db.DB().(storage.DBPgxEnginePool).Pool()
	require.Eventually(t, func() bool {
		var count int
		err := pool.QueryRow(ctx, `SELECT count(*) FROM pg_stat_activity WHERE datname=current_database() AND wait_event_type='Lock' AND query ILIKE '%FOR UPDATE%'`).Scan(&count)
		return err == nil && count >= want
	}, 5*time.Second, 10*time.Millisecond, "expected %d PostgreSQL row-lock waiters", want)
}

type e2eUploadStrategy struct {
	maxSize int64
}

func (s *e2eUploadStrategy) CanUpload(_ context.Context, _ host.UploadContext) error {
	return nil
}

func (s *e2eUploadStrategy) GetConfig(_ context.Context, _ host.UploadContext) *host.FileUploadConfig {
	return &host.FileUploadConfig{
		UploadDir:         "tmp/uploads",
		MaxFileSize:       s.maxSize,
		AllowedExtensions: []string{".pdf"},
		AllowedMimeTypes: map[string][]string{
			".pdf": {"application/pdf"},
		},
	}
}

func (s *e2eUploadStrategy) GetAfterJobs(_ context.Context, _ host.UploadContext) ([]host.FileEventAfterJob, error) {
	return nil, nil
}

func TestIntegration_StandardUploadHandlerE2E(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	database, _, cleanup := hosttest.PrepareDB(ctx, t, "e2e_std_handler")
	defer cleanup(context.Background())

	sqlDB := stdlib.OpenDBFromPool(database.DB().(storage.DBPgxEnginePool).Pool())
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithTableName("outbox_schema_versions"))
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())

	driver := host.StorageDriverLocal
	if os.Getenv("TEST_S3_ENDPOINT") != "" {
		driver = host.StorageDriverS3
	}
	cfg := storageConfig(t, ctx, driver)

	worker := newQueue(t, database)
	runtime, err := host.NewOriginalRuntime(cfg, host.OriginalRuntimeDeps{
		Database:    database,
		Transaction: transaction.New(database.DB()),
		Outbox:      worker,
	})
	require.NoError(t, err)
	require.NoError(t, worker.RegisterJobs(runtime.Jobs...))
	stopWorker := startWorker(t, ctx, worker)
	defer stopWorker()

	user := host.NewUserID()
	uploadHandler := host.NewUploadHandler(
		runtime.Uploader,
		runtime.Files,
		runtime.TusStore,
		gologger.Discard(),
		func(_ context.Context, metadata map[string]string) (host.UploadContext, error) {
			return host.UploadContext{
				UserID:    101,
				UserUUID:  user,
				Metadata:  metadata,
				SessionID: "e2e-session",
			}, nil
		},
		func(path string) string { return "/files/" + path },
	)
	uploadHandler.RegisterStrategy("default", &e2eUploadStrategy{maxSize: 20 * 1024 * 1024})

	stdHandler, err := host.NewStandardUploadHandler(uploadHandler, "/files")
	require.NoError(t, err)

	// Construct payload: > 5 MiB (5 MiB chunk 1 + trailing chunk 2)
	pdfHead := []byte("%PDF-1.7\n")
	pdfTail := []byte("\n%%EOF\n")
	chunk1Size := 5 * 1024 * 1024 // 5 MiB exactly (standard TUS chunk)
	chunk2Size := 1024 * 100      // 100 KiB
	totalSize := chunk1Size + chunk2Size
	filler := bytes.Repeat([]byte("0"), totalSize-len(pdfHead)-len(pdfTail))
	payload := append(append(pdfHead, filler...), pdfTail...)
	require.Len(t, payload, totalSize)

	// Step 1: POST /files/tus (Create upload session)
	metadata := map[string]string{
		"filename":    "large.pdf",
		"entity_type": "admin",
		"entity_id":   "42",
		"file_type":   "pdf",
		"context":     "default",
	}
	parts := make([]string, 0, len(metadata))
	for k, v := range metadata {
		parts = append(parts, k+" "+base64.StdEncoding.EncodeToString([]byte(v)))
	}

	createReq := httptest.NewRequestWithContext(ctx, http.MethodPost, "/files/tus", nil)
	createReq.Header.Set("Tus-Resumable", "1.0.0")
	createReq.Header.Set("Upload-Length", strconv.Itoa(totalSize))
	createReq.Header.Set("Upload-Metadata", strings.Join(parts, ","))
	createRec := httptest.NewRecorder()
	stdHandler.ServeHTTP(createRec, createReq)

	require.Equal(t, http.StatusCreated, createRec.Code, createRec.Body.String())
	location := createRec.Header().Get("Location")
	require.NotEmpty(t, location)

	// Step 2: PATCH Chunk 1 (5 MiB payload -> must NOT fail with 413!)
	patch1Req := httptest.NewRequestWithContext(ctx, http.MethodPatch, location, bytes.NewReader(payload[:chunk1Size]))
	patch1Req.Header.Set("Tus-Resumable", "1.0.0")
	patch1Req.Header.Set("Content-Type", "application/offset+octet-stream")
	patch1Req.Header.Set("Upload-Offset", "0")
	patch1Rec := httptest.NewRecorder()
	stdHandler.ServeHTTP(patch1Rec, patch1Req)

	require.Equal(t, http.StatusNoContent, patch1Rec.Code, patch1Rec.Body.String())
	require.Equal(t, strconv.Itoa(chunk1Size), patch1Rec.Header().Get("Upload-Offset"))

	// Step 3: HEAD (Verify offset resumption)
	headReq := httptest.NewRequestWithContext(ctx, http.MethodHead, location, nil)
	headReq.Header.Set("Tus-Resumable", "1.0.0")
	headRec := httptest.NewRecorder()
	stdHandler.ServeHTTP(headRec, headReq)

	require.Equal(t, http.StatusOK, headRec.Code)
	require.Equal(t, strconv.Itoa(chunk1Size), headRec.Header().Get("Upload-Offset"))
	require.Equal(t, strconv.Itoa(totalSize), headRec.Header().Get("Upload-Length"))

	// Step 4: PATCH Chunk 2 (Final bytes)
	patch2Req := httptest.NewRequestWithContext(ctx, http.MethodPatch, location, bytes.NewReader(payload[chunk1Size:]))
	patch2Req.Header.Set("Tus-Resumable", "1.0.0")
	patch2Req.Header.Set("Content-Type", "application/offset+octet-stream")
	patch2Req.Header.Set("Upload-Offset", strconv.Itoa(chunk1Size))
	patch2Rec := httptest.NewRecorder()
	stdHandler.ServeHTTP(patch2Rec, patch2Req)

	require.Equal(t, http.StatusNoContent, patch2Rec.Code, patch2Rec.Body.String())
	require.Equal(t, strconv.Itoa(totalSize), patch2Rec.Header().Get("Upload-Offset"))

	// Step 5: POST /complete
	completeReq := httptest.NewRequestWithContext(ctx, http.MethodPost, location+"/complete", nil)
	completeReq.Header.Set("Tus-Resumable", "1.0.0")
	completeRec := httptest.NewRecorder()
	stdHandler.ServeHTTP(completeRec, completeReq)

	require.Equal(t, http.StatusAccepted, completeRec.Code, completeRec.Body.String())
	var decoded struct {
		File struct {
			ID int64 `json:"id"`
		} `json:"file"`
	}
	require.NoError(t, json.Unmarshal(completeRec.Body.Bytes(), &decoded))
	require.Positive(t, decoded.File.ID)
	fileID := decoded.File.ID

	// Step 6: Idempotency of /complete (repeat must return same File ID)
	repeatCompleteReq := httptest.NewRequestWithContext(ctx, http.MethodPost, location+"/complete", nil)
	repeatCompleteReq.Header.Set("Tus-Resumable", "1.0.0")
	repeatCompleteRec := httptest.NewRecorder()
	stdHandler.ServeHTTP(repeatCompleteRec, repeatCompleteReq)

	require.Equal(t, http.StatusAccepted, repeatCompleteRec.Code, repeatCompleteRec.Body.String())
	var repeatDecoded struct {
		File struct {
			ID int64 `json:"id"`
		} `json:"file"`
	}
	require.NoError(t, json.Unmarshal(repeatCompleteRec.Body.Bytes(), &repeatDecoded))
	require.Equal(t, fileID, repeatDecoded.File.ID)

	// Step 7: Wait for outbox worker to finalize file into storage
	waitEmpty(t, ctx, worker)

	// Step 8: Verify file in database and storage
	file, err := runtime.Files.GetByID(ctx, fileID)
	require.NoError(t, err)
	require.Positive(t, file.ID)
	require.Empty(t, file.GetData().Uploader)
	require.Len(t, file.GetData().Presets, 1)

	preset, found := file.GetData().Presets[host.PresetName("main")]
	require.True(t, found)
	sum := sha256.Sum256(payload)
	require.Equal(t, hex.EncodeToString(sum[:]), preset.ChecksumSHA256)
	require.EqualValues(t, len(payload), file.Size)

	requireBytes(t, ctx, runtime.Storage, file.GetFullPath(), payload)
}
