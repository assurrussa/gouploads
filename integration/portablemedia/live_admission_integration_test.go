//go:build integration

package portablemedia_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"image"
	_ "image/jpeg"
	_ "image/png"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	logger "github.com/assurrussa/gologger"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	sendresizefilejob "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	uploadfilejob "github.com/assurrussa/gouploads/domain/files/outbox/upload_file"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
	testshelpers "github.com/assurrussa/gouploads/domain/files/tests"
	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
	sendresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
	uploadfile "github.com/assurrussa/gouploads/domain/files/usecases/command/upload_file"
	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
	eventstream "github.com/assurrussa/gouploads/internal/events"
)

// TestIntegrationLiveAdmissionClientChain uses actual media-resizer/Picodata,
// PostgreSQL file rows, local final storage, AWS SDK source presigning, and real
// vipsthumbnail/ffmpeg processing. Only source HTTP and callback delivery are
// fixtures. The collector exposes persisted outbox payloads for deterministic
// manual delivery; it does not claim to test the host's queue scheduler.
//
// The caller owns one isolated runtime and supplies TEST_MEDIA_RESIZER_URL,
// TEST_MEDIA_RESIZER_TOKEN, TEST_MEDIA_RESIZER_ISOLATED=1, and hosttest's explicit
// loopback TEST_PSQL_* settings. A short local OUTBOX_RESERVE_FOR (3s) bounds the
// thirty real failed media attempts before admission reconciliation.
func TestIntegrationLiveAdmissionClientChain(t *testing.T) {
	endpoint := strings.TrimRight(os.Getenv("TEST_MEDIA_RESIZER_URL"), "/")
	if endpoint == "" {
		t.Skip("explicit TEST_MEDIA_RESIZER_URL is required for the live admission chain")
	}
	require.Equal(t, "1", os.Getenv("TEST_MEDIA_RESIZER_ISOLATED"), "live chain requires a caller-owned disposable runtime")
	parsed, err := url.Parse(endpoint)
	require.NoError(t, err)
	require.Equal(t, "http", parsed.Scheme)
	require.Nil(t, parsed.User)
	require.True(t, net.ParseIP(parsed.Hostname()).IsLoopback(), "refusing a non-loopback media runtime")
	require.Empty(t, parsed.Path)
	token := os.Getenv("TEST_MEDIA_RESIZER_TOKEN")
	require.NotEmpty(t, token)
	postgresHost := os.Getenv("TEST_PSQL_ADDRESS_LOCAL")
	if postgresHost == "" {
		postgresHost = os.Getenv("TEST_PSQL_ADDRESS")
	}
	require.True(t, net.ParseIP(postgresHost).IsLoopback(), "hosttest must target an explicit disposable loopback database")
	_, err = exec.LookPath("ffmpeg")
	require.NoError(t, err, "live video fixture requires real ffmpeg")

	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Minute)
	defer cancel()
	database, _, cleanup := hosttest.PrepareDB(ctx, t, "liveadmission")
	defer cleanup(context.Background())
	tx := transaction.New(database.DB())
	repo, err := host.NewFileRepo(database, tx)
	require.NoError(t, err)
	root := t.TempDir()
	fixture := newLiveAdmissionFixture(t, root)
	defer fixture.Close()

	transport := &liveAdmissionTransport{client: fixture.Client()}
	resizer := clientresizer.Must(clientresizer.NewOptions(transport, endpoint+"/jobs", endpoint+"/jobs", logger.Discard(),
		clientresizer.WithImageResizerToken(token), clientresizer.WithVideoResizerToken(token),
		clientresizer.WithArtifactClient(fixture.Client())))
	cfg := host.StorageConfig{
		Driver: host.StorageDriverLocal,
		Public: host.StoragePublicConfig{Prefix: "media/v1"},
		Local:  host.StorageLocalConfig{Root: root, BaseURL: fixture.URL, SourceBaseURL: fixture.URL},
		Image: host.ImagePipelineConfig{
			ResizerHost: endpoint + "/jobs", WebhookCallbackHost: fixture.URL + "/callback",
			Presets: []host.ImagePresetConfig{{Name: "main", Format: "png", Width: 1, Height: 1, Fit: "cover", Quality: 82}},
		},
		Video: host.VideoPipelineConfig{
			ResizerHost: endpoint + "/jobs", WebhookCallbackHost: fixture.URL + "/callback",
			Presets: []host.VideoPresetConfig{{
				Name: "main", Format: "mp4", Width: 32, Height: 24, Quality: 82,
				Preview: host.PresetPreviewConfig{Enabled: true, Width: 16, Height: 12, Format: "png"},
			}},
		},
	}
	storage, err := host.NewStorage(cfg)
	require.NoError(t, err)
	resolver := &liveAdmissionResolver{current: liveSourceResolver(t, fixture.URL, 15*time.Minute)}
	outbox := &outboxCollector{}
	listener := listenresizefile.Must(listenresizefile.NewOptions(repo, outbox, eventstream.Discard{}, logger.Discard()))
	sender := sendresizefile.Must(sendresizefile.NewOptions(repo, resizer, resolver, eventstream.Discard{}, cfg.Image, cfg.Video, logger.Discard(), outbox, listener))
	sendJob := sendresizefilejob.Must(sendresizefilejob.NewOptions(sender, logger.Discard()))
	finalizer := uploadfile.Must(uploadfile.NewOptions(tx, repo, resizer, eventstream.Discard{}, logger.Discard(), storage, outbox,
		uploadfile.WithBaseFolder("media/v1"), uploadfile.WithDeliveryBaseURL(fixture.URL)))
	uploadJob := uploadfilejob.Must(uploadfilejob.NewOptions(finalizer, logger.Discard()))
	imageBody, err := io.ReadAll(testshelpers.CreateTestImage(t))
	require.NoError(t, err)

	t.Run("lost reply regenerated signature callback and poll", func(t *testing.T) {
		file := seedLiveMediaFile(t, ctx, repo, root, "tmp/live-image.png", imageBody, model.FileTypeImage, "image/png")
		payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(file.ID, file.GetFullPath(), false))
		require.NoError(t, err)
		transport.loseNext = true
		require.Error(t, sendJob.Handle(ctx, payload), "accepted response must be lost only at the client boundary")
		require.Empty(t, outbox.TakeAll(sendresizefilejob.JobName), "uncertain POST cannot persist a guessed job ID")
		unchanged, err := repo.GetByID(ctx, file.ID)
		require.NoError(t, err)
		require.Equal(t, shared.FileUploadTaskStatusProcessing, unchanged.GetData().Uploader.Status)
		first := transport.last(t)
		resolver.current = liveSourceResolver(t, fixture.URL, 16*time.Minute)
		require.NoError(t, sendJob.Handle(ctx, payload))
		second := transport.last(t)
		require.Equal(t, first.response.JobID, second.response.JobID)
		require.Equal(t, first.payload.IdempotencyKey, second.payload.IdempotencyKey)
		require.NotEqual(t, first.payload.Source.URL, second.payload.Source.URL, "real SDK presigning must change expiry/signature")
		require.Equal(t, strconv.FormatInt(file.ID, 10), second.payload.IdempotencyKey)
		continuation := outbox.Take(t, sendresizefilejob.JobName)
		persisted, err := sendresizefilejob.UnmarshalPayload(continuation)
		require.NoError(t, err)
		require.NotNil(t, persisted.JobID)
		require.Equal(t, second.response.JobID, *persisted.JobID)
		require.NotNil(t, persisted.PollDeadline)
		require.NotContains(t, continuation, "X-Amz-", "poll continuation must not retain signed source URL")

		changed := second.payload
		changed.Presets = append([]sendresizefile.Preset(nil), second.payload.Presets...)
		changed.Presets[0].Width++
		body, err := sendresizefile.MarshalPayload(changed)
		require.NoError(t, err)
		_, err = resizer.SendResize(ctx, clientresizer.Request{TypeMedia: "image", Data: body})
		var admissionErr *clientresizer.AdmissionError
		require.ErrorAs(t, err, &admissionErr)
		require.Equal(t, http.StatusConflict, admissionErr.StatusCode)
		require.False(t, admissionErr.Retryable())
		waitLiveMediaStatus(t, ctx, resizer, second.response.JobID, "image", "done")
		callback := fixture.callback(t, ctx, second.response.JobID)
		require.Equal(t, strconv.FormatInt(file.ID, 10), callback.IdempotencyKey)
		require.Equal(t, strconv.FormatInt(file.ID, 10), callback.Metadata["fileId"])
		require.NoError(t, sendJob.Handle(ctx, continuation), "persisted GET continuation must recover the result")
		_, err = listener.Handle(ctx, callback.request(t))
		require.NoError(t, err)
		queued := outbox.TakeAll(uploadfilejob.JobName)
		require.Len(t, queued, 2, "callback and poll may both schedule finalization before the row is completed")
		for _, raw := range queued {
			require.NoError(t, uploadJob.Handle(ctx, raw))
		}
		final := assertLiveFinalFile(t, ctx, repo, root, file.ID, "main")
		main := final.GetData().Presets[shared.FilePresetMainName]
		assertLiveImage(t, filepath.Join(root, filepath.FromSlash(main.RelativePath)), 1, 1)
		_, err = listener.Handle(ctx, listenresizefile.Request{ExternalID: file.ID, Status: "failed"})
		require.NoError(t, err, "late terminal failure must not downgrade completed storage")
		require.NoError(t, sendJob.Handle(ctx, continuation))
		_, err = listener.Handle(ctx, callback.request(t))
		require.NoError(t, err)
		require.Empty(t, outbox.TakeAll(uploadfilejob.JobName))
		after, err := repo.GetByID(ctx, file.ID)
		require.NoError(t, err)
		require.Equal(t, final, after, "late poll/callback must preserve the committed final row")
	})

	t.Run("real video and image preview through polling", func(t *testing.T) {
		videoPath := filepath.Join(t.TempDir(), "source.mp4")
		command := exec.CommandContext(ctx, "ffmpeg", "-hide_banner", "-loglevel", "error", "-f", "lavfi", "-i", "color=c=blue:s=32x24:d=1", "-c:v", "libx264", "-threads", "1", "-pix_fmt", "yuv420p", "-movflags", "+faststart", videoPath)
		output, err := command.CombinedOutput()
		require.NoError(t, err, "real video fixture: %s", output)
		video, err := os.ReadFile(videoPath)
		require.NoError(t, err)
		file := seedLiveMediaFile(t, ctx, repo, root, "tmp/live-video.mp4", video, model.FileTypeVideo, "video/mp4")
		response, err := sender.Handle(ctx, sendresizefile.Request{FileID: file.ID, FilePath: file.GetFullPath()})
		require.NoError(t, err)
		continuation := outbox.Take(t, sendresizefilejob.JobName)
		waitLiveMediaStatus(t, ctx, resizer, response.JobID, "video", "done")
		require.NoError(t, sendJob.Handle(ctx, continuation))
		require.NoError(t, uploadJob.Handle(ctx, outbox.Take(t, uploadfilejob.JobName)))
		final := assertLiveFinalFile(t, ctx, repo, root, file.ID, "main", "main_preview")
		preview := final.GetData().Presets[shared.PresetName("main_preview")]
		require.True(t, preview.IsPreview)
		assertLiveImage(t, filepath.Join(root, filepath.FromSlash(preview.RelativePath)), 16, 12)
		main := final.GetData().Presets[shared.FilePresetMainName]
		mainBytes, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(main.RelativePath)))
		require.NoError(t, err)
		require.Contains(t, string(mainBytes[:min(len(mainBytes), 32)]), "ftyp")
		callback := fixture.callback(t, ctx, response.JobID)
		_, err = listener.Handle(ctx, callback.request(t))
		require.NoError(t, err)
		require.Empty(t, outbox.TakeAll(uploadfilejob.JobName), "late video callback cannot repeat finalization")
	})

	t.Run("expired accepted source fails durably and new upload gets fresh identity", func(t *testing.T) {
		resolver.current = liveSourceResolver(t, fixture.URL, 15*time.Minute)
		file := seedLiveMediaFile(t, ctx, repo, root, "tmp/live-expired.png", imageBody, model.FileTypeImage, "image/png")
		fixture.mu.Lock()
		fixture.failPath = "/source/" + file.GetFullPath()
		fixture.mu.Unlock()
		response, err := sender.Handle(ctx, sendresizefile.Request{FileID: file.ID, FilePath: file.GetFullPath()})
		require.NoError(t, err)
		continuation := outbox.Take(t, sendresizefilejob.JobName)
		initial := transport.last(t)
		resolver.current = liveSourceResolver(t, fixture.URL, 16*time.Minute)
		replay, err := sender.Handle(ctx, sendresizefile.Request{FileID: file.ID, FilePath: file.GetFullPath()})
		require.NoError(t, err)
		require.Equal(t, response.JobID, replay.JobID)
		refreshed := transport.last(t)
		require.NotEqual(t, initial.payload.Source.URL, refreshed.payload.Source.URL)
		_ = outbox.TakeAll(sendresizefilejob.JobName)
		waitLiveMediaStatus(t, ctx, resizer, response.JobID, "image", "failed")
		fixture.mu.Lock()
		requests := append([]string(nil), fixture.failedQueries...)
		fixture.mu.Unlock()
		require.NotEmpty(t, requests)
		initialURL, err := url.Parse(initial.payload.Source.URL)
		require.NoError(t, err)
		for _, query := range requests {
			require.Equal(t, initialURL.RawQuery, query, "replay must never refresh the original persisted source URL")
		}
		require.NoError(t, sendJob.Handle(ctx, continuation), "terminal failure must persist into the real file repository")
		failed, err := repo.GetByID(ctx, file.ID)
		require.NoError(t, err)
		require.Equal(t, shared.FileUploadTaskStatusFailed, failed.GetData().Uploader.Status)
		require.Empty(t, failed.GetData().Presets)
		requireLocalFileBody(t, root, file.GetFullPath(), imageBody)
		require.NoError(t, sendJob.Handle(ctx, continuation))
		require.Empty(t, outbox.TakeAll(uploadfilejob.JobName))
		terminalReplay, err := sender.Handle(ctx, sendresizefile.Request{FileID: file.ID, FilePath: file.GetFullPath()})
		require.NoError(t, err)
		require.Equal(t, response.JobID, terminalReplay.JobID, "failed replay must retain its original logical binding")
		_ = outbox.TakeAll(sendresizefilejob.JobName)

		fixture.mu.Lock()
		fixture.allowFresh = true
		fixture.mu.Unlock()
		fresh := seedLiveMediaFile(t, ctx, repo, root, file.GetFullPath(), imageBody, model.FileTypeImage, "image/png")
		require.NotEqual(t, file.ID, fresh.ID, "new logical attempt is a distinct upload identity")
		newResponse, err := sender.Handle(ctx, sendresizefile.Request{FileID: fresh.ID, FilePath: fresh.GetFullPath()})
		require.NoError(t, err)
		require.NotEqual(t, response.JobID, newResponse.JobID)
		require.NotEqual(t, initial.payload.IdempotencyKey, transport.last(t).payload.IdempotencyKey)
		freshContinuation := outbox.Take(t, sendresizefilejob.JobName)
		waitLiveMediaStatus(t, ctx, resizer, newResponse.JobID, "image", "done")
		require.NoError(t, sendJob.Handle(ctx, freshContinuation))
		require.NoError(t, uploadJob.Handle(ctx, outbox.Take(t, uploadfilejob.JobName)))
		final := assertLiveFinalFile(t, ctx, repo, root, fresh.ID, "main")
		freshCallback := fixture.callback(t, ctx, newResponse.JobID)
		require.Equal(t, strconv.FormatInt(fresh.ID, 10), freshCallback.IdempotencyKey)
		_, err = listener.Handle(ctx, listenresizefile.Request{ExternalID: file.ID, Status: "failed"})
		require.NoError(t, err)
		after, err := repo.GetByID(ctx, fresh.ID)
		require.NoError(t, err)
		require.Equal(t, final, after, "a delayed old-attempt failure cannot affect a fresh upload")
	})
}

type liveAdmissionResolver struct{ current host.SourceURLResolver }

func (r *liveAdmissionResolver) Resolve(ctx context.Context, source string) (string, error) {
	return r.current.Resolve(ctx, source)
}

func liveSourceResolver(t *testing.T, endpoint string, ttl time.Duration) host.SourceURLResolver {
	t.Helper()
	resolver, err := host.NewSourceURLResolver(host.StorageConfig{
		Driver: host.StorageDriverS3,
		Public: host.StoragePublicConfig{Prefix: "media/v1", BaseURL: endpoint},
		S3: host.StorageS3Config{
			Endpoint: endpoint, Region: "us-east-1", Bucket: "final", StagingBucket: "source",
			AccessKey: "SYNTHETIC", SecretKey: "synthetic-local-fixture-only", ForcePathStyle: true,
			SourceURLTTL: ttl, Timeout: 10 * time.Second, MaxRetries: 1,
		},
		Tus: host.StorageTusConfig{StagingPrefix: "tmp", SessionTTL: time.Hour, CleanupInterval: time.Hour},
	})
	require.NoError(t, err)
	return resolver
}

func TestLiveSourceResolverFixture(t *testing.T) {
	const endpoint = "http://127.0.0.1:18085"
	var previous *url.URL
	for _, ttl := range []time.Duration{15 * time.Minute, 16 * time.Minute} {
		resolver := liveSourceResolver(t, endpoint, ttl)
		signed, err := resolver.Resolve(context.Background(), "tmp/live-image.png")
		require.NoError(t, err)
		parsed, err := url.Parse(signed)
		require.NoError(t, err)
		require.Equal(t, "http", parsed.Scheme)
		require.Equal(t, "127.0.0.1:18085", parsed.Host)
		require.Equal(t, "/source/tmp/live-image.png", parsed.Path)
		require.Equal(t, "AWS4-HMAC-SHA256", parsed.Query().Get("X-Amz-Algorithm"))
		require.Equal(t, strconv.Itoa(int(ttl.Seconds())), parsed.Query().Get("X-Amz-Expires"))
		require.NotEmpty(t, parsed.Query().Get("X-Amz-Signature"))
		if previous != nil {
			require.Equal(t, previous.Path, parsed.Path)
			require.NotEqual(t, previous.Query().Get("X-Amz-Signature"), parsed.Query().Get("X-Amz-Signature"))
		}
		previous = parsed
	}
}

func seedLiveMediaFile(t *testing.T, ctx context.Context, repo *host.FileRepo, root, relative string, body []byte, kind model.FileType, mime string) model.File {
	t.Helper()
	abs := filepath.Join(root, filepath.FromSlash(relative))
	require.NoError(t, os.MkdirAll(filepath.Dir(abs), 0o755))
	require.NoError(t, os.WriteFile(abs, body, 0o600))
	file := testshelpers.CreateFile(t)
	file.ID, file.URL, file.FileName, file.FolderPath = 0, "", path.Base(relative), path.Dir(relative)
	file.OriginalFileName, file.Size, file.FileType, file.MimeType = file.FileName, int64(len(body)), kind, mime
	file.Data.Uploader.AfterJobs = nil
	id, err := repo.Create(ctx, file)
	require.NoError(t, err)
	file, err = repo.GetByID(ctx, id)
	require.NoError(t, err)
	return file
}

func waitLiveMediaStatus(t *testing.T, ctx context.Context, resizer *clientresizer.Service, id outboxtypes.JobID, mediaType, want string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Minute)
	for time.Now().Before(deadline) {
		result, err := resizer.GetJob(ctx, clientresizer.JobRequest{JobID: id, TypeMedia: mediaType})
		require.NoError(t, err)
		require.NotNil(t, result.Admission, "worker must have admission enabled in the disposable runtime")
		if result.Status == want {
			return
		}
		require.Contains(t, []string{"queued", "running", "unknown"}, result.Status)
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(time.Second):
		}
	}
	t.Fatalf("logical job did not reach %s within the bounded live test", want)
}

func assertLiveFinalFile(t *testing.T, ctx context.Context, repo *host.FileRepo, root string, id int64, names ...string) model.File {
	t.Helper()
	file, err := repo.GetByID(ctx, id)
	require.NoError(t, err)
	require.Empty(t, file.GetData().Uploader)
	require.Len(t, file.GetData().Presets, len(names))
	for _, name := range names {
		preset, found := file.GetData().Presets[shared.PresetName(name)]
		require.True(t, found, "missing real artifact %s", name)
		data, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(preset.RelativePath)))
		require.NoError(t, err)
		require.NotEmpty(t, data)
	}
	return file
}

func assertLiveImage(t *testing.T, name string, width, height int) {
	t.Helper()
	data, err := os.ReadFile(name)
	require.NoError(t, err)
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	require.NoError(t, err)
	require.Equal(t, width, cfg.Width)
	require.Equal(t, height, cfg.Height)
}

type liveAdmissionObservation struct {
	payload  sendresizefile.Payload
	response clientresizer.Response
}

type liveAdmissionTransport struct {
	client   *http.Client
	loseNext bool
	posts    []liveAdmissionObservation
}

func (c *liveAdmissionTransport) Do(request *http.Request) (*http.Response, error) {
	if request.Method != http.MethodPost {
		return c.client.Do(request)
	}
	body, err := io.ReadAll(request.Body)
	if err != nil {
		return nil, err
	}
	request.Body = io.NopCloser(bytes.NewReader(body))
	response, err := c.client.Do(request)
	if err != nil || response.StatusCode != http.StatusAccepted {
		return response, err
	}
	raw, readErr := io.ReadAll(response.Body)
	_ = response.Body.Close()
	if readErr != nil {
		return nil, readErr
	}
	var observed liveAdmissionObservation
	if err := json.Unmarshal(body, &observed.payload); err != nil {
		return nil, err
	}
	if err := json.Unmarshal(raw, &observed.response); err != nil {
		return nil, err
	}
	c.posts = append(c.posts, observed)
	if c.loseNext {
		c.loseNext = false
		return nil, errors.New("synthetic accepted response loss")
	}
	response.Body = io.NopCloser(bytes.NewReader(raw))
	return response, nil
}

func (c *liveAdmissionTransport) last(t *testing.T) liveAdmissionObservation {
	t.Helper()
	require.NotEmpty(t, c.posts)
	return c.posts[len(c.posts)-1]
}

type liveAdmissionWebhook struct {
	JobID          outboxtypes.JobID           `json:"job_id"`
	IdempotencyKey string                      `json:"idempotency_key"`
	Status         string                      `json:"status"`
	Metadata       map[string]any              `json:"metadata"`
	Artifacts      []clientresizer.JobArtifact `json:"artifacts"`
}

func (event liveAdmissionWebhook) request(t *testing.T) listenresizefile.Request {
	t.Helper()
	key, ok := event.Metadata["fileId"].(string)
	require.True(t, ok)
	id, err := strconv.ParseInt(key, 10, 64)
	require.NoError(t, err)
	request := listenresizefile.Request{ExternalID: id, Status: event.Status, Metadata: event.Metadata}
	for _, artifact := range event.Artifacts {
		request.Artifacts = append(request.Artifacts, listenresizefile.Artifact{
			Preset: artifact.Preset, URL: artifact.URL,
			MediaType: artifact.MediaType, ContentType: artifact.ContentType, Size: artifact.Size, ExpireAt: artifact.ExpireAt, Metadata: artifact.Metadata,
		})
	}
	return request
}

type liveAdmissionFixture struct {
	*httptest.Server
	mu            sync.Mutex
	failPath      string
	failedQueries []string
	allowFresh    bool
	callbacks     chan liveAdmissionWebhook
}

func newLiveAdmissionFixture(t *testing.T, root string) *liveAdmissionFixture {
	t.Helper()
	fixture := &liveAdmissionFixture{callbacks: make(chan liveAdmissionWebhook, 20)}
	files := http.StripPrefix("/source", http.FileServer(http.Dir(root)))
	fixture.Server = httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method == http.MethodPost && request.URL.Path == "/callback" {
			var event liveAdmissionWebhook
			if err := json.NewDecoder(io.LimitReader(request.Body, 1<<20)).Decode(&event); err != nil {
				http.Error(writer, "invalid fixture callback", http.StatusBadRequest)
				return
			}
			select {
			case fixture.callbacks <- event:
				writer.WriteHeader(http.StatusNoContent)
			default:
				http.Error(writer, "fixture callback capacity", http.StatusServiceUnavailable)
			}
			return
		}
		fixture.mu.Lock()
		failed := request.URL.Path == fixture.failPath
		if failed {
			original := len(fixture.failedQueries) == 0 || request.URL.RawQuery == fixture.failedQueries[0]
			fixture.failedQueries = append(fixture.failedQueries, request.URL.RawQuery)
			failed = original || !fixture.allowFresh
		}
		fixture.mu.Unlock()
		if failed {
			http.Error(writer, "synthetic expired source", http.StatusForbidden)
			return
		}
		files.ServeHTTP(writer, request)
	}))
	return fixture
}

func (f *liveAdmissionFixture) callback(t *testing.T, ctx context.Context, jobID outboxtypes.JobID) liveAdmissionWebhook {
	t.Helper()
	timer := time.NewTimer(30 * time.Second)
	defer timer.Stop()
	select {
	case event := <-f.callbacks:
		require.Equal(t, jobID, event.JobID)
		require.Equal(t, "done", event.Status)
		return event
	case <-timer.C:
		t.Fatal("real media callback not received")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	return liveAdmissionWebhook{}
}
