//go:build integration

package portablemedia_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/gofiber/fiber/v3"
	"github.com/google/uuid"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	deletedfilejob "github.com/assurrussa/gouploads/domain/files/outbox/deleted_file"
	sendresizefilejob "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	uploadfilejob "github.com/assurrussa/gouploads/domain/files/outbox/upload_file"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/service/uploadservice"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	deletefile "github.com/assurrussa/gouploads/domain/files/usecases/command/delete_file"
	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
	eventstream "github.com/assurrussa/gouploads/internal/events"
)

const (
	testAccessKey = "minioadmin"
	testSecretKey = "minioadmin"
	testRegion    = "us-east-1"
)

type scenario struct {
	name                  string
	originalName          string
	sourceBody            []byte
	artifacts             []artifactSpec
	separateStagingBucket bool
	partialFailure        bool
	testReplacement       bool
}

type artifactSpec struct {
	preset      string
	path        string
	mediaType   string
	contentType string
	body        []byte
	metadata    map[string]any
}

type processedUpload struct {
	file   model.File
	userID host.UserID
}

func TestIntegrationPortableS3Media(t *testing.T) {
	imageBody, err := io.ReadAll(testshelpers.CreateTestImage(t))
	require.NoError(t, err)
	videoBody := []byte{
		0x00, 0x00, 0x00, 0x18, 'f', 't', 'y', 'p', 'i', 's', 'o', 'm',
		0x00, 0x00, 0x00, 0x00, 'i', 's', 'o', 'm', 'm', 'p', '4', '2',
	}
	pdfBody := []byte("%PDF-1.4\n1 0 obj\n<<>>\nendobj\n%%EOF\n")

	tests := []scenario{
		{
			name:            "one bucket image retry replacement",
			originalName:    "camera-original.png",
			sourceBody:      imageBody,
			partialFailure:  true,
			testReplacement: true,
			artifacts: []artifactSpec{
				{
					preset:      "original",
					path:        "/image/original",
					mediaType:   "image",
					contentType: "image/png",
					body:        imageBody,
					metadata:    map[string]any{"target_width": 2, "target_height": 2},
				},
				{
					preset:      "main",
					path:        "/image/main",
					mediaType:   "image",
					contentType: "image/png",
					body:        imageBody,
					metadata:    map[string]any{"target_width": 2, "target_height": 2},
				},
				{
					preset:      "thumbnail",
					path:        "/image/thumbnail",
					mediaType:   "image",
					contentType: "image/png",
					body:        imageBody,
					metadata:    map[string]any{"thumbnail": true, "target_width": 1, "target_height": 1},
				},
			},
		},
		{
			name:                  "separate staging bucket video",
			originalName:          "source-video.mp4",
			sourceBody:            videoBody,
			separateStagingBucket: true,
			artifacts: []artifactSpec{
				{
					preset:      "main",
					path:        "/video/main",
					mediaType:   "video",
					contentType: "video/mp4",
					body:        videoBody,
				},
			},
		},
		{
			name:         "one bucket pdf",
			originalName: "source-document.pdf",
			sourceBody:   pdfBody,
			artifacts: []artifactSpec{
				{
					preset:      "main",
					path:        "/pdf/main",
					mediaType:   "document",
					contentType: "application/pdf",
					body:        pdfBody,
				},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			runScenario(t, tt)
		})
	}
}

func runScenario(t *testing.T, tt scenario) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	endpoint := envOr("TEST_S3_ENDPOINT", "http://127.0.0.1:9090")
	client := newS3Client(endpoint)
	readinessCtx, readinessCancel := context.WithTimeout(ctx, 5*time.Second)
	defer readinessCancel()
	_, err := client.ListBuckets(readinessCtx, &s3.ListBucketsInput{})
	require.NoError(t, err, "mandatory MinIO dependency is unavailable")

	suffix := strings.ToLower(strings.ReplaceAll(uuid.NewString(), "-", ""))[:12]
	publicBucket := "portable-public-" + suffix
	stagingBucket := publicBucket
	configuredStagingBucket := ""
	if tt.separateStagingBucket {
		stagingBucket = "portable-staging-" + suffix
		configuredStagingBucket = stagingBucket
	}
	createBucket(t, ctx, client, publicBucket)
	if stagingBucket != publicBucket {
		createBucket(t, ctx, client, stagingBucket)
	}
	setPublicMediaPolicy(t, ctx, client, publicBucket)
	t.Cleanup(func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		cleanupBucket(t, cleanupCtx, client, stagingBucket)
		if publicBucket != stagingBucket {
			cleanupBucket(t, cleanupCtx, client, publicBucket)
		}
	})

	publicBaseURL := strings.TrimRight(endpoint, "/") + "/" + publicBucket
	cfg := host.StorageConfig{
		Driver: host.StorageDriverS3,
		Public: host.StoragePublicConfig{
			BaseURL: publicBaseURL,
			Prefix:  "media/v1",
		},
		S3: host.StorageS3Config{
			Endpoint:       endpoint,
			Region:         testRegion,
			Bucket:         publicBucket,
			StagingBucket:  configuredStagingBucket,
			AccessKey:      testAccessKey,
			SecretKey:      testSecretKey,
			ForcePathStyle: true,
			SourceURLTTL:   15 * time.Minute,
			Timeout:        10 * time.Second,
			MaxRetries:     3,
		},
		Tus: host.StorageTusConfig{
			StagingPrefix:   "staging/v1/tus",
			PartSize:        host.ParseSize("5MB"),
			SessionTTL:      24 * time.Hour,
			LeaseTTL:        30 * time.Second,
			CleanupInterval: time.Hour,
		},
	}
	checker, err := host.NewStorageContractChecker(cfg)
	require.NoError(t, err)
	report, err := checker.Check(ctx)
	require.NoError(t, err, "portable storage checker report: %+v", report)
	require.NotEmpty(t, report.Checks)
	for _, check := range report.Checks {
		require.True(t, check.Success, "storage check %s failed: %s", check.Name, check.Detail)
	}

	database, _, databaseCleanup := hosttest.PrepareDB(ctx, t, "portablemedia")
	defer databaseCleanup(context.Background())
	tx := transaction.New(database.DB())
	repo, err := host.NewFileRepo(database, tx)
	require.NoError(t, err)
	storage, err := host.NewStorage(cfg)
	require.NoError(t, err)
	tusStore, err := host.NewTusStore(cfg, database)
	require.NoError(t, err)
	sourceResolver, err := host.NewSourceURLResolver(cfg)
	require.NoError(t, err)
	userID := host.NewUserID()

	artifactServer := newArtifactServer(t, tt.artifacts, "")
	defer artifactServer.Close()
	outbox := &outboxCollector{}
	events := eventstream.Discard{}
	resizer := clientresizer.Must(clientresizer.NewOptions(
		noopTransport{},
		artifactServer.URL+"/jobs",
		artifactServer.URL+"/jobs",
		logger.Discard(),
		clientresizer.WithImageResizerToken("must-not-leak-image-token"),
		clientresizer.WithVideoResizerToken("must-not-leak-video-token"),
		clientresizer.WithArtifactClient(artifactServer.Client()),
	))
	listenUseCase := listenresizefile.Must(listenresizefile.NewOptions(repo, outbox, events, logger.Discard()))
	uploadUseCase := uploadfile.Must(uploadfile.NewOptions(
		tx,
		repo,
		resizer,
		events,
		logger.Discard(),
		storage,
		outbox,
		uploadfile.WithBaseFolder("media/v1"),
		uploadfile.WithDeliveryBaseURL(publicBaseURL),
	))
	deleteUseCase := deletefile.Must(deletefile.NewOptions(
		tx,
		repo,
		events,
		logger.Discard(),
		storage,
		outbox,
		deletefile.WithDeliveryBaseURL(publicBaseURL),
	))
	uploadService := uploadservice.Must(uploadservice.NewOptions(
		tx,
		outbox,
		repo,
		logger.Discard(),
		storage,
	))
	handler := host.NewUploadHandler(
		uploadService,
		repo,
		tusStore,
		logger.Discard(),
		func(_ context.Context, metadata map[string]string) (host.UploadContext, error) {
			return host.UploadContext{
				UserID:    101,
				UserUUID:  userID,
				SessionID: "portable-media-e2e",
				Metadata:  metadata,
			}, nil
		},
		func(key string) string { return publicObjectURL(publicBaseURL, key) },
	)
	httpApp := fiber.New()
	host.NewFiberUploadHandler(handler).RegisterGroupRoutes("/files/", httpApp)
	uploadJob := uploadfilejob.Must(uploadfilejob.NewOptions(uploadUseCase, logger.Discard()))
	deleteJob := deletedfilejob.Must(deletedfilejob.NewOptions(deleteUseCase, logger.Discard()))
	runtime := uploadRuntime{
		cfg:            cfg,
		s3:             client,
		publicBucket:   publicBucket,
		stagingBucket:  stagingBucket,
		publicBaseURL:  publicBaseURL,
		tusStore:       tusStore,
		sourceResolver: sourceResolver,
		repo:           repo,
		listener:       listenUseCase,
		uploadJob:      uploadJob,
		deleteJob:      deleteJob,
		outbox:         outbox,
		artifactServer: artifactServer,
		httpApp:        httpApp,
		userID:         userID,
	}

	first := processUpload(t, ctx, runtime, tt, 0, false, nil)
	verifyFinalObjects(t, ctx, client, publicBucket, publicBaseURL, first.file, tt.artifacts)
	artifactServer.RequireNoSecretHeaders(t)

	if !tt.testReplacement {
		return
	}

	verifyForeignReplacementRejected(t, ctx, runtime, tt, first)
	legacyKeys := seedLegacyPresetObjects(t, ctx, runtime, first.file)
	artifactServer.FailOnce(failurePath(tt))
	second := processUpload(t, ctx, runtime, tt, first.file.ID, tt.partialFailure, &first)
	oldURL := publicObjectURL(publicBaseURL, first.file.GetFullPath())
	newURL := publicObjectURL(publicBaseURL, second.file.GetFullPath())
	require.NotEqual(t, first.file.Slug, second.file.Slug)
	require.NotEqual(t, oldURL, newURL)
	requireHTTPStatus(t, ctx, http.MethodGet, newURL, "", http.StatusOK)
	verifyFileObjectsAbsent(t, ctx, runtime, first.file)
	for _, key := range legacyKeys {
		requireObjectMissing(t, ctx, runtime.s3, runtime.publicBucket, key)
	}
}

type uploadRuntime struct {
	cfg            host.StorageConfig
	s3             *s3.Client
	publicBucket   string
	stagingBucket  string
	publicBaseURL  string
	tusStore       host.TusStore
	sourceResolver host.SourceURLResolver
	repo           *host.FileRepo
	listener       *listenresizefile.UseCase
	uploadJob      *uploadfilejob.Job
	deleteJob      *deletedfilejob.Job
	outbox         *outboxCollector
	artifactServer *artifactHTTPServer
	httpApp        *fiber.App
	userID         host.UserID
}

func processUpload(
	t *testing.T,
	ctx context.Context,
	runtime uploadRuntime,
	tt scenario,
	deletedID int64,
	expectPartialFailure bool,
	replaced *processedUpload,
) processedUpload {
	t.Helper()
	upload := completeTusUpload(t, ctx, runtime, tt, 42, deletedID, http.StatusAccepted)
	wantStagingPath := upload.stagingPath
	require.Equal(t, "source"+path.Ext(tt.originalName), path.Base(wantStagingPath))
	require.NotEqual(t, path.Base(wantStagingPath), tt.originalName)

	file, err := runtime.repo.GetByID(ctx, upload.fileID)
	require.NoError(t, err)
	require.Equal(t, path.Base(wantStagingPath), file.FileName)
	require.Equal(t, tt.originalName, file.OriginalFileName)
	runtime.outbox.Take(t, sendresizefilejob.JobName)

	head, err := runtime.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(runtime.stagingBucket),
		Key:    aws.String(wantStagingPath),
	})
	require.NoError(t, err)
	require.Equal(t, "private,no-store", aws.ToString(head.CacheControl))
	requireHTTPStatus(
		t,
		ctx,
		http.MethodGet,
		objectURL(runtime.cfg.S3.Endpoint, runtime.stagingBucket, wantStagingPath),
		"",
		http.StatusForbidden,
	)

	signedSourceURL, err := runtime.sourceResolver.Resolve(ctx, wantStagingPath)
	require.NoError(t, err)
	require.Equal(t, mustURLHost(t, runtime.cfg.S3.Endpoint), mustURLHost(t, signedSourceURL))
	require.NotEmpty(t, mustURLQuery(t, signedSourceURL))
	signedResponse, err := doRequest(ctx, http.MethodGet, signedSourceURL, "")
	require.NoError(t, err)
	signedBody, readErr := io.ReadAll(signedResponse.Body)
	require.NoError(t, signedResponse.Body.Close())
	require.NoError(t, readErr)
	require.Equal(t, http.StatusOK, signedResponse.StatusCode)
	require.Equal(t, sha256Hex(tt.sourceBody), sha256Hex(signedBody))

	artifacts := make([]host.ResizeArtifactInput, 0, len(tt.artifacts))
	for _, artifact := range tt.artifacts {
		artifacts = append(artifacts, host.ResizeArtifactInput{
			Preset:      artifact.preset,
			URL:         runtime.artifactServer.SignedURL(artifact.path),
			MediaType:   artifact.mediaType,
			ContentType: artifact.contentType,
			Size:        int64(len(artifact.body)),
			ExpireAt:    time.Now().Add(time.Hour),
			Metadata:    artifact.metadata,
		})
	}
	_, err = runtime.listener.Handle(ctx, host.BuildListenResizeRequest(host.ListenResizeInput{
		ExternalID: file.ID,
		Status:     "done",
		Attempt:    1,
		Timestamp:  time.Now().UTC(),
		Artifacts:  artifacts,
	}))
	require.NoError(t, err)
	uploadPayload := runtime.outbox.Take(t, uploadfilejob.JobName)

	if expectPartialFailure {
		err = runtime.uploadJob.Handle(ctx, uploadPayload)
		require.Error(t, err)
		cleanupPayloads := runtime.outbox.TakeAll(deletedfilejob.JobName)
		require.Empty(t, cleanupPayloads, "a retryable attempt must not schedule cleanup for deterministic final keys")
		if replaced != nil {
			verifyFinalObjects(t, ctx, runtime.s3, runtime.publicBucket, runtime.publicBaseURL, replaced.file, tt.artifacts)
		}
	}

	require.NoError(t, runtime.uploadJob.Handle(ctx, uploadPayload))
	cleanupPayloads := runtime.outbox.TakeAll(deletedfilejob.JobName)
	wantCleanupJobs := 1
	if deletedID > 0 {
		wantCleanupJobs++
	}
	require.Len(t, cleanupPayloads, wantCleanupJobs, "successful finalization must schedule independent cleanup jobs")
	finalized, err := runtime.repo.GetByID(ctx, file.ID)
	require.NoError(t, err)
	verifyFinalObjects(t, ctx, runtime.s3, runtime.publicBucket, runtime.publicBaseURL, finalized, tt.artifacts)
	if replaced != nil {
		verifyFinalObjects(t, ctx, runtime.s3, runtime.publicBucket, runtime.publicBaseURL, replaced.file, tt.artifacts)
	}
	executeCleanupJobs(t, ctx, runtime, cleanupPayloads, wantStagingPath, deletedID)

	// A duplicate webhook/outbox delivery after completion must be a no-op. In
	// particular, it must never schedule deletion of a deterministic final key.
	require.NoError(t, runtime.uploadJob.Handle(ctx, uploadPayload))
	require.Empty(t, runtime.outbox.TakeAll(deletedfilejob.JobName))

	_, err = runtime.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(runtime.stagingBucket),
		Key:    aws.String(wantStagingPath),
	})
	require.Error(t, err, "staging source must be deleted after successful DB commit")

	completed, err := runtime.repo.GetByID(ctx, file.ID)
	require.NoError(t, err)
	require.Empty(t, completed.URL)
	require.Empty(t, completed.GetData().Uploader)
	require.NotContains(t, completed.FolderPath, runtime.cfg.S3.Endpoint)
	return processedUpload{file: completed, userID: runtime.userID}
}

type tusHTTPResult struct {
	fileID      int64
	sessionID   string
	stagingPath string
}

func completeTusUpload(
	t *testing.T,
	ctx context.Context,
	runtime uploadRuntime,
	tt scenario,
	objectID int64,
	deletedID int64,
	wantCompleteStatus int,
) tusHTTPResult {
	t.Helper()

	metadata := map[string]string{
		"filename":    tt.originalName,
		"entity_type": host.ObjectTypeAdmin.String(),
		"entity_id":   strconv.FormatInt(objectID, 10),
		"file_type":   tt.artifacts[0].mediaType,
	}
	if deletedID > 0 {
		metadata["replace_file_id"] = strconv.FormatInt(deletedID, 10)
	}

	createResponse, _ := sendFiberRequest(t, ctx, runtime.httpApp, http.MethodPost, "/files/tus", nil, map[string]string{
		"Tus-Resumable":   "1.0.0",
		"Upload-Length":   strconv.FormatInt(int64(len(tt.sourceBody)), 10),
		"Upload-Metadata": encodeTusMetadata(metadata),
	})
	require.Equal(t, http.StatusCreated, createResponse.StatusCode)
	location := createResponse.Header.Get("Location")
	require.NotEmpty(t, location)
	sessionID := path.Base(location)

	patchResponse, _ := sendFiberRequest(t, ctx, runtime.httpApp, http.MethodPatch, location, tt.sourceBody, map[string]string{
		"Tus-Resumable": "1.0.0",
		"Content-Type":  "application/offset+octet-stream",
		"Upload-Offset": "0",
	})
	require.Equal(t, http.StatusNoContent, patchResponse.StatusCode)
	require.Equal(t, strconv.Itoa(len(tt.sourceBody)), patchResponse.Header.Get("Upload-Offset"))

	session, err := runtime.tusStore.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, path.Join("staging/v1/tus", sessionID, "source"+path.Ext(tt.originalName)), session.Path)

	completeResponse, responseBody := sendFiberRequest(
		t,
		ctx,
		runtime.httpApp,
		http.MethodPost,
		location+"/complete",
		nil,
		map[string]string{"Tus-Resumable": "1.0.0"},
	)
	require.Equal(t, wantCompleteStatus, completeResponse.StatusCode, "completion response: %s", responseBody)

	result := tusHTTPResult{sessionID: sessionID, stagingPath: session.Path}
	if wantCompleteStatus == http.StatusAccepted {
		var payload struct {
			File struct {
				ID int64 `json:"id"`
			} `json:"file"`
		}
		require.NoError(t, json.Unmarshal(responseBody, &payload))
		require.Positive(t, payload.File.ID)
		result.fileID = payload.File.ID
		retained, err := runtime.tusStore.Get(ctx, sessionID)
		require.NoError(t, err)
		require.Equal(t, "ready", string(retained.Status))
		retryResponse, retryBody := sendFiberRequest(t, ctx, runtime.httpApp, http.MethodPost,
			location+"/complete", nil, map[string]string{"Tus-Resumable": "1.0.0"})
		require.Equal(t, http.StatusAccepted, retryResponse.StatusCode, "retry response: %s", retryBody)
		require.NoError(t, json.Unmarshal(retryBody, &payload))
		require.Equal(t, result.fileID, payload.File.ID)
	}

	return result
}

func sendFiberRequest(
	t *testing.T,
	ctx context.Context,
	app *fiber.App,
	method string,
	requestPath string,
	body []byte,
	headers map[string]string,
) (*http.Response, []byte) {
	t.Helper()

	request := httptest.NewRequestWithContext(ctx, method, requestPath, bytes.NewReader(body))
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	response, err := app.Test(request)
	require.NoError(t, err)
	responseBody, err := io.ReadAll(response.Body)
	require.NoError(t, err)
	require.NoError(t, response.Body.Close())

	return response, responseBody
}

func encodeTusMetadata(metadata map[string]string) string {
	parts := make([]string, 0, len(metadata))
	for key, value := range metadata {
		parts = append(parts, key+" "+base64.StdEncoding.EncodeToString([]byte(value)))
	}
	return strings.Join(parts, ",")
}

func verifyForeignReplacementRejected(
	t *testing.T,
	ctx context.Context,
	runtime uploadRuntime,
	tt scenario,
	existing processedUpload,
) {
	t.Helper()

	rejected := completeTusUpload(t, ctx, runtime, tt, 43, existing.file.ID, http.StatusBadRequest)
	require.Zero(t, rejected.fileID)
	session, err := runtime.tusStore.Get(ctx, rejected.sessionID)
	require.NoError(t, err)
	require.Equal(t, host.TusStatusReady, session.Status)
	_, err = runtime.s3.HeadObject(ctx, &s3.HeadObjectInput{
		Bucket: aws.String(runtime.stagingBucket),
		Key:    aws.String(rejected.stagingPath),
	})
	require.NoError(t, err, "rejected replacement must not delete the staging object")
	require.Empty(t, runtime.outbox.TakeAll(deletedfilejob.JobName))
	require.Empty(t, runtime.outbox.TakeAll(sendresizefilejob.JobName))
	verifyFinalObjects(t, ctx, runtime.s3, runtime.publicBucket, runtime.publicBaseURL, existing.file, tt.artifacts)
}

func executeCleanupJobs(
	t *testing.T,
	ctx context.Context,
	runtime uploadRuntime,
	payloads []string,
	stagingPath string,
	deletedID int64,
) {
	t.Helper()

	foundStaging := false
	foundReplacement := false
	for _, payloadData := range payloads {
		payload, err := deletedfilejob.UnmarshalPayload(payloadData)
		require.NoError(t, err)
		switch payload.FileID {
		case 0:
			foundStaging = true
			require.Equal(t, stagingPath, payload.FilePath)
			require.Empty(t, payload.ObjectType)
			require.Zero(t, payload.ObjectID)
		default:
			foundReplacement = true
			require.Equal(t, deletedID, payload.FileID)
			require.Empty(t, payload.FilePath)
			require.Equal(t, host.ObjectTypeAdmin, payload.ObjectType)
			require.Equal(t, host.ObjectID(42), payload.ObjectID)
		}
		require.NoError(t, runtime.deleteJob.Handle(ctx, payloadData))
		require.NoError(t, runtime.deleteJob.Handle(ctx, payloadData), "cleanup job must be idempotent")
	}
	require.True(t, foundStaging)
	require.Equal(t, deletedID > 0, foundReplacement)
}

func seedLegacyPresetObjects(t *testing.T, ctx context.Context, runtime uploadRuntime, file model.File) []string {
	t.Helper()

	keys := make([]string, 0, len(file.GetData().Presets))
	for presetName, preset := range file.GetData().Presets {
		name := strings.TrimSpace(preset.PresetName)
		if name == "" {
			name = presetName.String()
		}
		if name == "" || shared.PresetName(name) == shared.FilePresetMainName {
			continue
		}
		key := path.Join(file.FolderPath, name, file.FileName)
		_, err := runtime.s3.PutObject(ctx, &s3.PutObjectInput{
			Bucket:      aws.String(runtime.publicBucket),
			Key:         aws.String(key),
			Body:        strings.NewReader("legacy preset"),
			ContentType: aws.String("application/octet-stream"),
		})
		require.NoError(t, err)
		keys = append(keys, key)
	}

	return keys
}

func verifyFileObjectsAbsent(t *testing.T, ctx context.Context, runtime uploadRuntime, file model.File) {
	t.Helper()

	requireObjectMissing(t, ctx, runtime.s3, runtime.publicBucket, file.GetFullPath())
	for _, preset := range file.GetData().Presets {
		if preset.RelativePath != "" {
			requireObjectMissing(t, ctx, runtime.s3, runtime.publicBucket, preset.RelativePath)
		}
	}
}

func requireObjectMissing(t *testing.T, ctx context.Context, client *s3.Client, bucket, key string) {
	t.Helper()

	_, err := client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: aws.String(bucket), Key: aws.String(key)})
	require.Error(t, err, "object must be absent: %s", key)
}

func verifyFinalObjects(
	t *testing.T,
	ctx context.Context,
	client *s3.Client,
	bucket string,
	publicBaseURL string,
	file model.File,
	artifacts []artifactSpec,
) {
	t.Helper()
	require.NotEmpty(t, file.GetData().Presets)
	for _, artifact := range artifacts {
		preset, found := file.GetData().Presets[shared.PresetName(artifact.preset)]
		require.True(t, found)
		require.Empty(t, preset.URL)
		require.Equal(t, sha256Hex(artifact.body), preset.ChecksumSHA256)
		require.Equal(t, artifact.contentType, preset.MimeType)
		require.True(t, strings.HasPrefix(
			preset.RelativePath,
			path.Join("media/v1", host.ObjectTypeAdmin.String(), "42", file.Slug)+"/",
		))

		head, err := client.HeadObject(ctx, &s3.HeadObjectInput{
			Bucket: aws.String(bucket),
			Key:    aws.String(preset.RelativePath),
		})
		require.NoError(t, err)
		require.Equal(t, artifact.contentType, aws.ToString(head.ContentType))
		require.Equal(t, "public,max-age=31536000,immutable", aws.ToString(head.CacheControl))
		require.Equal(t, int64(len(artifact.body)), aws.ToInt64(head.ContentLength))

		publicURL := publicObjectURL(publicBaseURL, preset.RelativePath)
		requireHTTPStatus(t, ctx, http.MethodHead, publicURL, "", http.StatusOK)
		response, err := doRequest(ctx, http.MethodGet, publicURL, "")
		require.NoError(t, err)
		body, readErr := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, readErr)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.Equal(t, artifact.body, body)
		require.Equal(t, artifact.contentType, response.Header.Get("Content-Type"))
		require.Equal(t, "public,max-age=31536000,immutable", response.Header.Get("Cache-Control"))

		rangeResponse, err := doRequest(ctx, http.MethodGet, publicURL, "bytes=0-3")
		require.NoError(t, err)
		rangeBody, readErr := io.ReadAll(rangeResponse.Body)
		require.NoError(t, rangeResponse.Body.Close())
		require.NoError(t, readErr)
		require.Equal(t, http.StatusPartialContent, rangeResponse.StatusCode)
		require.Equal(t, artifact.body[:min(4, len(artifact.body))], rangeBody)
	}

	mainPreset, found := file.GetData().Presets[shared.FilePresetMainName]
	require.True(t, found)
	require.Equal(t, mainPreset.RelativePath, file.GetFullPath())
	require.Empty(t, file.URL)
}

type queuedJob struct {
	name    string
	payload string
}

type outboxCollector struct {
	mu   sync.Mutex
	jobs []queuedJob
}

func (c *outboxCollector) Put(
	_ context.Context,
	name string,
	payload string,
	_ time.Time,
) (outboxtypes.JobID, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.jobs = append(c.jobs, queuedJob{name: name, payload: payload})
	return outboxtypes.NewJobID(), nil
}

func (c *outboxCollector) Take(t *testing.T, name string) string {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	for index, job := range c.jobs {
		if job.name != name {
			continue
		}
		c.jobs = append(c.jobs[:index], c.jobs[index+1:]...)
		return job.payload
	}
	require.FailNow(t, "outbox job not found", "job=%s queued=%v", name, c.jobs)
	return ""
}

func (c *outboxCollector) TakeAll(name string) []string {
	c.mu.Lock()
	defer c.mu.Unlock()
	payloads := make([]string, 0)
	remaining := make([]queuedJob, 0, len(c.jobs))
	for _, job := range c.jobs {
		if job.name == name {
			payloads = append(payloads, job.payload)
			continue
		}
		remaining = append(remaining, job)
	}
	c.jobs = remaining
	return payloads
}

type noopTransport struct{}

func (noopTransport) Do(*http.Request) (*http.Response, error) {
	return &http.Response{StatusCode: http.StatusAccepted, Body: io.NopCloser(strings.NewReader(`{}`))}, nil
}

type artifactHTTPServer struct {
	*httptest.Server
	mu                sync.Mutex
	artifacts         map[string]artifactSpec
	failuresRemaining map[string]int
	secretHeaders     []string
}

func newArtifactServer(t *testing.T, artifacts []artifactSpec, failOncePath string) *artifactHTTPServer {
	t.Helper()
	server := &artifactHTTPServer{
		artifacts:         make(map[string]artifactSpec, len(artifacts)),
		failuresRemaining: make(map[string]int),
	}
	for _, artifact := range artifacts {
		server.artifacts[artifact.path] = artifact
	}
	if failOncePath != "" {
		server.failuresRemaining[failOncePath] = 1
	}
	server.Server = httptest.NewServer(http.HandlerFunc(server.handle))
	return server
}

func (s *artifactHTTPServer) SignedURL(artifactPath string) string {
	return s.URL + artifactPath + "?signature=integration-secret&expires=9999999999"
}

func (s *artifactHTTPServer) FailOnce(artifactPath string) {
	if artifactPath == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.failuresRemaining[artifactPath]++
}

func (s *artifactHTTPServer) handle(writer http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, header := range []string{"Authorization", "X-API-Token"} {
		if request.Header.Get(header) != "" {
			s.secretHeaders = append(s.secretHeaders, header)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
	}
	if request.URL.Query().Get("signature") == "" {
		writer.WriteHeader(http.StatusUnauthorized)
		return
	}
	artifact, found := s.artifacts[request.URL.Path]
	if !found {
		writer.WriteHeader(http.StatusNotFound)
		return
	}
	if s.failuresRemaining[request.URL.Path] > 0 {
		s.failuresRemaining[request.URL.Path]--
		writer.WriteHeader(http.StatusServiceUnavailable)
		return
	}
	writer.Header().Set("Content-Type", artifact.contentType)
	writer.Header().Set("Content-Length", fmt.Sprintf("%d", len(artifact.body)))
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(artifact.body)
}

func (s *artifactHTTPServer) RequireNoSecretHeaders(t *testing.T) {
	t.Helper()
	s.mu.Lock()
	defer s.mu.Unlock()
	require.Empty(t, s.secretHeaders)
}

func newS3Client(endpoint string) *s3.Client {
	config := aws.Config{
		Region:      testRegion,
		Credentials: credentials.NewStaticCredentialsProvider(testAccessKey, testSecretKey, ""),
		HTTPClient:  &http.Client{Timeout: 10 * time.Second},
	}
	return s3.NewFromConfig(config, func(options *s3.Options) {
		options.UsePathStyle = true
		options.BaseEndpoint = aws.String(endpoint)
	})
}

func createBucket(t *testing.T, ctx context.Context, client *s3.Client, bucket string) {
	t.Helper()
	_, err := client.CreateBucket(ctx, &s3.CreateBucketInput{Bucket: aws.String(bucket)})
	require.NoError(t, err)
}

func setPublicMediaPolicy(t *testing.T, ctx context.Context, client *s3.Client, bucket string) {
	t.Helper()
	policy := map[string]any{
		"Version": "2012-10-17",
		"Statement": []map[string]any{{
			"Sid":       "PublicReadMediaV1",
			"Effect":    "Allow",
			"Principal": map[string]any{"AWS": []string{"*"}},
			"Action":    []string{"s3:GetObject"},
			"Resource":  []string{"arn:aws:s3:::" + bucket + "/media/v1/*"},
		}},
	}
	data, err := json.Marshal(policy)
	require.NoError(t, err)
	_, err = client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{
		Bucket: aws.String(bucket),
		Policy: aws.String(string(data)),
	})
	require.NoError(t, err)
}

func cleanupBucket(t *testing.T, ctx context.Context, client *s3.Client, bucket string) {
	t.Helper()
	listed, err := client.ListObjectsV2(ctx, &s3.ListObjectsV2Input{Bucket: aws.String(bucket)})
	if err == nil && len(listed.Contents) > 0 {
		objects := make([]s3types.ObjectIdentifier, 0, len(listed.Contents))
		for _, object := range listed.Contents {
			objects = append(objects, s3types.ObjectIdentifier{Key: object.Key})
		}
		_, err = client.DeleteObjects(ctx, &s3.DeleteObjectsInput{
			Bucket: aws.String(bucket),
			Delete: &s3types.Delete{Objects: objects},
		})
	}
	if err != nil {
		t.Logf("cleanup bucket %s objects: %v", bucket, err)
	}
	if _, err = client.DeleteBucket(ctx, &s3.DeleteBucketInput{Bucket: aws.String(bucket)}); err != nil {
		t.Logf("delete bucket %s: %v", bucket, err)
	}
}

func failurePath(tt scenario) string {
	if !tt.partialFailure || len(tt.artifacts) < 2 {
		return ""
	}
	for _, artifact := range tt.artifacts {
		if artifact.preset == "thumbnail" {
			return artifact.path
		}
	}
	return tt.artifacts[len(tt.artifacts)-1].path
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func sha256Hex(body []byte) string {
	sum := sha256.Sum256(body)
	return hex.EncodeToString(sum[:])
}

func objectURL(endpoint, bucket, key string) string {
	return strings.TrimRight(endpoint, "/") + "/" + bucket + "/" + strings.TrimLeft(key, "/")
}

func publicObjectURL(publicBaseURL, key string) string {
	return strings.TrimRight(publicBaseURL, "/") + "/" + strings.TrimLeft(key, "/")
}

func doRequest(ctx context.Context, method, rawURL, byteRange string) (*http.Response, error) {
	request, err := http.NewRequestWithContext(ctx, method, rawURL, nil)
	if err != nil {
		return nil, err
	}
	if byteRange != "" {
		request.Header.Set("Range", byteRange)
	}
	return http.DefaultClient.Do(request)
}

func requireHTTPStatus(
	t *testing.T,
	ctx context.Context,
	method string,
	rawURL string,
	byteRange string,
	want int,
) {
	t.Helper()
	response, err := doRequest(ctx, method, rawURL, byteRange)
	require.NoError(t, err)
	defer response.Body.Close()
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.Equal(t, want, response.StatusCode)
}

func requireHTTPNotSuccessful(t *testing.T, ctx context.Context, rawURL string) {
	t.Helper()
	response, err := doRequest(ctx, http.MethodGet, rawURL, "")
	require.NoError(t, err)
	defer response.Body.Close()
	_, err = io.Copy(io.Discard, response.Body)
	require.NoError(t, err)
	require.NotEqual(t, http.StatusOK, response.StatusCode)
}

func mustURLHost(t *testing.T, rawURL string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	return request.URL.Host
}

func mustURLQuery(t *testing.T, rawURL string) string {
	t.Helper()
	request, err := http.NewRequest(http.MethodGet, rawURL, nil)
	require.NoError(t, err)
	return request.URL.RawQuery
}

func min(left, right int) int {
	if left < right {
		return left
	}
	return right
}
