//go:build integration

package portablemedia_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/assurrussa/goshared/pkg/logger"
	transporthttp "github.com/assurrussa/goshared/pkg/transport/http"
	inmemeventstream "github.com/assurrussa/goshared/services/event-stream/in-mem"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/gofiber/fiber/v3"
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
	sendresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
)

func TestIntegrationLocalMedia(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	imageBody, err := io.ReadAll(testshelpers.CreateTestImage(t))
	require.NoError(t, err)
	artifacts := []artifactSpec{
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
	}
	scenario := scenario{
		originalName: "client-avatar.png",
		sourceBody:   imageBody,
		artifacts:    artifacts,
	}

	root := t.TempDir()
	fileServer := httptest.NewServer(http.FileServer(http.Dir(root)))
	defer fileServer.Close()

	cfg := host.StorageConfig{
		Driver: host.StorageDriverLocal,
		Public: host.StoragePublicConfig{Prefix: "media/v1"},
		Local: host.StorageLocalConfig{
			Root:          root,
			BaseURL:       fileServer.URL,
			SourceBaseURL: fileServer.URL,
		},
		Image: host.ImagePipelineConfig{
			DefaultFormat:       "png",
			ResizerHost:         "http://media-resizer.test/jobs",
			WebhookCallbackHost: "http://backend.test/api/v1/media-resize",
			Presets: []host.ImagePresetConfig{
				{Name: "original", Format: "png", Quality: 82},
				{Name: "main", Format: "png", Quality: 82},
				{Name: "thumbnail", Format: "png", Quality: 82},
			},
		},
		Video: host.VideoPipelineConfig{
			ResizerHost:         "http://media-resizer.test/jobs",
			WebhookCallbackHost: "http://backend.test/api/v1/media-resize",
			Presets: []host.VideoPresetConfig{
				{Name: "main", Format: "mp4", Quality: 82},
			},
		},
		Tus: host.StorageTusConfig{
			SessionTTL:      24 * time.Hour,
			CleanupInterval: time.Hour,
		},
	}

	database, _, databaseCleanup := hosttest.PrepareDB(ctx, t, "localmedia")
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

	artifactServer := newArtifactServer(t, artifacts, "")
	defer artifactServer.Close()
	sourceTransport := &sourceFetchTransport{expectedBody: imageBody, client: fileServer.Client()}
	resizer := clientresizer.Must(clientresizer.NewOptions(
		sourceTransport,
		artifactServer.URL+"/jobs",
		artifactServer.URL+"/jobs",
		logger.Discard(),
		clientresizer.WithArtifactClient(artifactServer.Client()),
	))
	outbox := &outboxCollector{}
	events := inmemeventstream.New()
	listener := listenresizefile.Must(listenresizefile.NewOptions(repo, outbox, events, logger.Discard()))
	uploadUseCase := uploadfile.Must(uploadfile.NewOptions(
		tx,
		repo,
		resizer,
		events,
		logger.Discard(),
		storage,
		outbox,
		uploadfile.WithBaseFolder("media/v1"),
		uploadfile.WithDeliveryBaseURL(fileServer.URL),
	))
	deleteUseCase := deletefile.Must(deletefile.NewOptions(
		tx,
		repo,
		events,
		logger.Discard(),
		storage,
		outbox,
		deletefile.WithDeliveryBaseURL(fileServer.URL),
	))
	sendUseCase := sendresizefile.Must(sendresizefile.NewOptions(
		repo,
		resizer,
		sourceResolver,
		events,
		cfg.Image,
		cfg.Video,
		logger.Discard(),
	))
	uploadService := uploadservice.Must(uploadservice.NewOptions(tx, outbox, repo, logger.Discard(), storage))
	userID := host.NewUserID()
	handler := host.NewUploadHandler(
		uploadService,
		repo,
		tusStore,
		logger.Discard(),
		func(_ context.Context, metadata map[string]string) (host.UploadContext, error) {
			return host.UploadContext{
				UserID:    101,
				UserUUID:  userID,
				SessionID: "local-media-e2e",
				Metadata:  metadata,
			}, nil
		},
		func(key string) string { return publicObjectURL(fileServer.URL, key) },
	)
	httpApp := fiber.New()
	host.NewFiberUploadHandler(handler).RegisterGroupRoutes("/files/", httpApp)
	runtime := localUploadRuntime{
		root:           root,
		publicBaseURL:  fileServer.URL,
		tusStore:       tusStore,
		repo:           repo,
		listener:       listener,
		sendJob:        sendresizefilejob.Must(sendresizefilejob.NewOptions(sendUseCase, logger.Discard())),
		uploadJob:      uploadfilejob.Must(uploadfilejob.NewOptions(uploadUseCase, logger.Discard())),
		deleteJob:      deletedfilejob.Must(deletedfilejob.NewOptions(deleteUseCase, logger.Discard())),
		outbox:         outbox,
		artifactServer: artifactServer,
		httpApp:        httpApp,
	}

	first := processLocalUpload(t, ctx, runtime, scenario, 0, false, nil)
	verifyLocalFinalFiles(t, ctx, runtime, first, artifacts)
	verifyLocalForeignReplacementRejected(t, ctx, runtime, scenario, first.ID)
	legacyPaths := seedLocalLegacyPresetFiles(t, runtime, first)

	artifactServer.FailOnce("/image/thumbnail")
	second := processLocalUpload(t, ctx, runtime, scenario, first.ID, true, &first)
	require.NotEqual(t, first.Slug, second.Slug)
	verifyLocalFilePathsAbsent(t, runtime, first)
	for _, legacyPath := range legacyPaths {
		_, err = os.Stat(filepath.Join(root, filepath.FromSlash(legacyPath)))
		require.ErrorIs(t, err, os.ErrNotExist)
	}
	verifyLocalFinalFiles(t, ctx, runtime, second, artifacts)
	require.Len(t, sourceTransport.sourceURLs, 2)
}

type localUploadRuntime struct {
	root           string
	publicBaseURL  string
	tusStore       host.TusStore
	repo           *host.FileRepo
	listener       *listenresizefile.UseCase
	sendJob        *sendresizefilejob.Job
	uploadJob      *uploadfilejob.Job
	deleteJob      *deletedfilejob.Job
	outbox         *outboxCollector
	artifactServer *artifactHTTPServer
	httpApp        *fiber.App
}

func processLocalUpload(
	t *testing.T,
	ctx context.Context,
	runtime localUploadRuntime,
	tt scenario,
	deletedID int64,
	expectPartialFailure bool,
	replaced *model.File,
) model.File {
	t.Helper()

	fileID := completeLocalTusUpload(t, ctx, runtime, tt, 42, deletedID, http.StatusAccepted)
	file, err := runtime.repo.GetByID(ctx, fileID)
	require.NoError(t, err)
	require.Equal(t, tt.originalName, file.OriginalFileName)
	require.True(t, strings.HasPrefix(file.GetFullPath(), "tmp/uploads/admin/42/"))
	require.NotEqual(t, tt.originalName, file.FileName)
	requireLocalFileBody(t, runtime.root, file.GetFullPath(), tt.sourceBody)

	require.NoError(t, runtime.sendJob.Handle(ctx, runtime.outbox.Take(t, sendresizefilejob.JobName)))

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
		require.Empty(t, runtime.outbox.TakeAll(deletedfilejob.JobName))
		if replaced != nil {
			verifyLocalFinalFiles(t, ctx, runtime, *replaced, tt.artifacts)
		}
	}

	require.NoError(t, runtime.uploadJob.Handle(ctx, uploadPayload))
	cleanupPayloads := runtime.outbox.TakeAll(deletedfilejob.JobName)
	wantCleanupJobs := 1
	if deletedID > 0 {
		wantCleanupJobs++
	}
	require.Len(t, cleanupPayloads, wantCleanupJobs)
	if replaced != nil {
		verifyLocalFinalFiles(t, ctx, runtime, *replaced, tt.artifacts)
	}
	executeLocalCleanupJobs(t, ctx, runtime, cleanupPayloads, file.GetFullPath(), deletedID)

	require.NoError(t, runtime.uploadJob.Handle(ctx, uploadPayload))
	require.Empty(t, runtime.outbox.TakeAll(deletedfilejob.JobName))
	_, err = os.Stat(filepath.Join(runtime.root, filepath.FromSlash(file.GetFullPath())))
	require.ErrorIs(t, err, os.ErrNotExist)

	completed, err := runtime.repo.GetByID(ctx, file.ID)
	require.NoError(t, err)
	require.Empty(t, completed.GetData().Uploader)
	require.Empty(t, completed.URL)
	return completed
}

func completeLocalTusUpload(
	t *testing.T,
	ctx context.Context,
	runtime localUploadRuntime,
	tt scenario,
	objectID int64,
	deletedID int64,
	wantCompleteStatus int,
) int64 {
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

	session, err := runtime.tusStore.Get(ctx, sessionID)
	require.NoError(t, err)
	require.Equal(t, filepath.Join(runtime.root, "tmp", "tus", sessionID, "data"), session.Path)

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
	if wantCompleteStatus != http.StatusAccepted {
		return 0
	}

	var payload struct {
		File struct {
			ID int64 `json:"id"`
		} `json:"file"`
	}
	require.NoError(t, json.Unmarshal(responseBody, &payload))
	require.Positive(t, payload.File.ID)
	_, err = runtime.tusStore.Get(ctx, sessionID)
	require.ErrorIs(t, err, host.ErrTusNotFound)
	return payload.File.ID
}

func verifyLocalForeignReplacementRejected(
	t *testing.T,
	ctx context.Context,
	runtime localUploadRuntime,
	tt scenario,
	fileID int64,
) {
	t.Helper()

	require.Zero(t, completeLocalTusUpload(t, ctx, runtime, tt, 43, fileID, http.StatusBadRequest))
	require.Empty(t, runtime.outbox.TakeAll(sendresizefilejob.JobName))
	require.Empty(t, runtime.outbox.TakeAll(deletedfilejob.JobName))
}

func executeLocalCleanupJobs(
	t *testing.T,
	ctx context.Context,
	runtime localUploadRuntime,
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
		if payload.FileID == 0 {
			foundStaging = true
			require.Equal(t, stagingPath, payload.FilePath)
		} else {
			foundReplacement = true
			require.Equal(t, deletedID, payload.FileID)
			require.Equal(t, host.ObjectTypeAdmin, payload.ObjectType)
			require.Equal(t, host.ObjectID(42), payload.ObjectID)
		}
		require.NoError(t, runtime.deleteJob.Handle(ctx, payloadData))
		require.NoError(t, runtime.deleteJob.Handle(ctx, payloadData))
	}
	require.True(t, foundStaging)
	require.Equal(t, deletedID > 0, foundReplacement)
}

func verifyLocalFinalFiles(
	t *testing.T,
	ctx context.Context,
	runtime localUploadRuntime,
	file model.File,
	artifacts []artifactSpec,
) {
	t.Helper()

	require.NotEmpty(t, file.GetData().Presets)
	for _, artifact := range artifacts {
		preset, found := file.GetData().Presets[shared.PresetName(artifact.preset)]
		require.True(t, found)
		require.Equal(t, sha256Hex(artifact.body), preset.ChecksumSHA256)
		require.True(t, strings.HasPrefix(
			preset.RelativePath,
			path.Join("uploads", "media/v1", host.ObjectTypeAdmin.String(), "42", file.Slug)+"/",
		))
		requireLocalFileBody(t, runtime.root, preset.RelativePath, artifact.body)

		response, err := doRequest(ctx, http.MethodGet, publicObjectURL(runtime.publicBaseURL, preset.RelativePath), "")
		require.NoError(t, err)
		body, readErr := io.ReadAll(response.Body)
		require.NoError(t, response.Body.Close())
		require.NoError(t, readErr)
		require.Equal(t, http.StatusOK, response.StatusCode)
		require.Equal(t, artifact.body, body)
	}
	mainPreset, found := file.GetData().Presets[shared.FilePresetMainName]
	require.True(t, found)
	require.Equal(t, mainPreset.RelativePath, file.GetFullPath())
}

func seedLocalLegacyPresetFiles(t *testing.T, runtime localUploadRuntime, file model.File) []string {
	t.Helper()

	paths := make([]string, 0, len(file.GetData().Presets))
	for presetName, preset := range file.GetData().Presets {
		name := strings.TrimSpace(preset.PresetName)
		if name == "" {
			name = presetName.String()
		}
		if name == "" || shared.PresetName(name) == shared.FilePresetMainName {
			continue
		}
		legacyPath := path.Join(file.FolderPath, name, file.FileName)
		absPath := filepath.Join(runtime.root, filepath.FromSlash(legacyPath))
		require.NoError(t, os.MkdirAll(filepath.Dir(absPath), 0o755))
		require.NoError(t, os.WriteFile(absPath, []byte("legacy preset"), 0o644))
		paths = append(paths, legacyPath)
	}
	return paths
}

func verifyLocalFilePathsAbsent(t *testing.T, runtime localUploadRuntime, file model.File) {
	t.Helper()

	paths := []string{file.GetFullPath()}
	for _, preset := range file.GetData().Presets {
		paths = append(paths, preset.RelativePath)
	}
	for _, relativePath := range paths {
		if relativePath == "" {
			continue
		}
		_, err := os.Stat(filepath.Join(runtime.root, filepath.FromSlash(relativePath)))
		require.ErrorIs(t, err, os.ErrNotExist, "path must be absent: %s", relativePath)
	}
}

func requireLocalFileBody(t *testing.T, root, relativePath string, want []byte) {
	t.Helper()

	data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(relativePath)))
	require.NoError(t, err)
	require.Equal(t, want, data)
}

type sourceFetchTransport struct {
	client       *http.Client
	expectedBody []byte
	sourceURLs   []string
}

func (t *sourceFetchTransport) DoWithRequestAndParse(
	ctx context.Context,
	request transporthttp.Request,
	data any,
) error {
	payloadData, err := io.ReadAll(request.Body)
	if err != nil {
		return fmt.Errorf("read resizer request: %w", err)
	}
	var payload sendresizefile.Payload
	if err := json.Unmarshal(payloadData, &payload); err != nil {
		return fmt.Errorf("decode resizer request: %w", err)
	}

	sourceRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, payload.Source.URL, nil)
	if err != nil {
		return fmt.Errorf("create source request: %w", err)
	}
	response, err := t.client.Do(sourceRequest)
	if err != nil {
		return fmt.Errorf("fetch source: %w", err)
	}
	defer response.Body.Close()
	if response.StatusCode != http.StatusOK {
		return fmt.Errorf("fetch source: unexpected status %d", response.StatusCode)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		return fmt.Errorf("read source: %w", err)
	}
	if !bytes.Equal(t.expectedBody, body) {
		return fmt.Errorf("source body mismatch: got %d bytes", len(body))
	}
	t.sourceURLs = append(t.sourceURLs, payload.Source.URL)

	result, ok := data.(*clientresizer.Response)
	if !ok {
		return fmt.Errorf("unexpected resizer response type %T", data)
	}
	result.JobID = outboxtypes.NewJobID()
	result.Status = "queued"
	return nil
}
