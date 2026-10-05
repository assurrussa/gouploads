//go:build integration

package portablemedia_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	gologger "github.com/assurrussa/gologger"
	"github.com/assurrussa/outbox/backends/pgsql/migrations"
	"github.com/assurrussa/outbox/backends/pgsql/repositories/jobsfailedrepo"
	"github.com/assurrussa/outbox/backends/pgsql/repositories/jobsrepo"
	"github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/assurrussa/outbox/outbox"
	outboxlogger "github.com/assurrussa/outbox/outbox/logger"
	outboxtypes "github.com/assurrussa/outbox/shared/types"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/domain/files/model"
	sendresizefilejob "github.com/assurrussa/gouploads/domain/files/outbox/send_resize_file"
	clientresizer "github.com/assurrussa/gouploads/domain/files/service/client_resizer"
	"github.com/assurrussa/gouploads/domain/files/shared"
	listenresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/listen_resize_file"
	sendresizefile "github.com/assurrussa/gouploads/domain/files/usecases/command/send_resize_file"
	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
	eventstream "github.com/assurrussa/gouploads/internal/events"
)

// TestIntegrationAdmissionContinuationRestart proves PostgreSQL persistence and
// real scheduling across worker-service reconstruction. It uses an explicit
// loopback HTTP fixture, not a real media processor. It does not claim abrupt
// process-crash recovery, exactly-once delivery, or object-store compatibility.
func TestIntegrationAdmissionContinuationRestart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	database, _, cleanup := hosttest.PrepareDB(ctx, t, "admissionrestart")
	defer cleanup(context.Background())
	engine, ok := database.DB().(storage.DBPgxEnginePool)
	require.True(t, ok, "integration fixture must expose its PostgreSQL pool")
	sqlDB := stdlib.OpenDBFromPool(engine.Pool())
	defer func() { require.NoError(t, sqlDB.Close()) }()
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithTableName("outbox_schema_versions"))
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	repo, err := host.NewFileRepo(database, transaction.New(database.DB()))
	require.NoError(t, err)
	root := t.TempDir()
	source := []byte("retained fixture source")
	file := seedLiveMediaFile(t, ctx, repo, root, "tmp/uploads/restart.png", source, model.FileTypeImage, "image/png")
	logicalID := outboxtypes.NewJobID()
	var posts, gets atomic.Int32
	var terminalGETAt atomic.Int64
	retrySent := make(chan time.Time, 1)
	fixture := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.Method == http.MethodPost && r.URL.Path == "/jobs":
			posts.Add(1)
			var request sendresizefile.Payload
			if json.NewDecoder(r.Body).Decode(&request) != nil || request.IdempotencyKey != strconv.FormatInt(file.ID, 10) {
				http.Error(w, "wrong logical identity", http.StatusConflict)
				return
			}
			w.WriteHeader(http.StatusAccepted)
			_ = json.NewEncoder(w).Encode(clientresizer.Response{JobID: logicalID, Status: "queued"})
		case r.Method == http.MethodGet && r.URL.Path == "/jobs/"+logicalID.String():
			if gets.Add(1) == 1 {
				retrySent <- time.Now()
				w.Header().Set("Retry-After", "2")
				w.WriteHeader(http.StatusTooManyRequests)
				return
			}
			terminalGETAt.Store(time.Now().UnixNano())
			_ = json.NewEncoder(w).Encode(clientresizer.JobResponse{
				JobID: logicalID, IdempotencyKey: strconv.FormatInt(file.ID, 10), Status: "failed",
			})
		default:
			http.Error(w, "unexpected fixture request", http.StatusNotFound)
		}
	}))
	defer fixture.Close()

	first := newAdmissionRestartQueue(t, database, fixture)
	payload, err := sendresizefilejob.MarshalPayload(sendresizefilejob.NewPayload(file.ID, file.GetFullPath(), false))
	require.NoError(t, err)
	before := time.Now()
	_, err = first.Put(ctx, sendresizefilejob.JobName, payload, before)
	require.NoError(t, err)
	stopFirst := runAdmissionRestartQueue(t, ctx, first)
	defer stopFirst()
	var continuation, continuationID string
	var availableAt time.Time
	require.EventuallyWithT(t, func(check *assert.CollectT) {
		require.EqualValues(check, 1, posts.Load(), "worker must POST the queued dispatch")
		const query = `SELECT id::text, payload, available_at FROM jobs WHERE name=$1 AND payload::jsonb ? 'jobId'`
		err := sqlDB.QueryRowContext(ctx, query, sendresizefilejob.JobName).
			Scan(&continuationID, &continuation, &availableAt)
		require.NoError(check, err)
		var count int
		require.NoError(check, sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM jobs`).Scan(&count))
		require.Equal(check, 1, count)
	}, 10*time.Second, 10*time.Millisecond, "real worker must acknowledge POST and persist its continuation")
	stopFirst()
	require.EqualValues(t, 1, posts.Load())
	require.Zero(t, gets.Load(), "future continuation must not be consumed before restart")
	stored, err := sendresizefilejob.UnmarshalPayload(continuation)
	require.NoError(t, err)
	require.Equal(t, file.ID, stored.FileID)
	require.Empty(t, stored.FilePath)
	require.NotNil(t, stored.JobID)
	require.Equal(t, logicalID, *stored.JobID)
	require.NotNil(t, stored.PollDeadline)
	require.WithinDuration(t, before.Add(7*24*time.Hour), *stored.PollDeadline, 10*time.Second)
	require.WithinDuration(t, before.Add(30*time.Second), availableAt, 10*time.Second)
	require.False(t, availableAt.Before(before.Add(30*time.Second)))
	require.True(t, stored.PollDeadline.After(availableAt))
	require.True(t, availableAt.After(time.Now()), "exercise an actual future queue deadline")

	// Rebuild the queue, repository, sender, HTTP client and listener. Neither a
	// collector nor direct Handle calls carry state across this boundary.
	second := newAdmissionRestartQueue(t, database, fixture)
	stopSecond := runAdmissionRestartQueue(t, ctx, second)
	defer stopSecond()
	var retryAt time.Time
	select {
	case retryAt = <-retrySent:
	case <-ctx.Done():
		t.Fatal("restarted worker did not deliver the persisted continuation")
	}
	require.False(t, retryAt.Before(availableAt), "scheduler must respect the persisted available_at")
	var retriedPayload string
	var nextAvailableAt time.Time
	require.Eventually(t, func() bool {
		const query = `SELECT payload, available_at FROM jobs WHERE id=$1 AND available_at >= $2`
		return sqlDB.QueryRowContext(ctx, query, continuationID, retryAt.Add(2*time.Second)).
			Scan(&retriedPayload, &nextAvailableAt) == nil
	}, time.Second, 10*time.Millisecond, "Retry-After must reschedule the same durable polling job")
	require.Equal(t, continuation, retriedPayload, "retry must preserve logical job, file ID and polling deadline")
	require.WithinDuration(t, retryAt.Add(2*time.Second), nextAvailableAt, time.Second)
	pending, err := repo.GetByID(ctx, file.ID)
	require.NoError(t, err)
	require.Equal(t, shared.FileUploadTaskStatusProcessing, pending.GetData().Uploader.Status)
	require.Eventually(t, func() bool {
		failed, err := repo.GetByID(ctx, file.ID)
		if err != nil || failed.GetData().Uploader.Status != shared.FileUploadTaskStatusFailed {
			return false
		}
		var count int
		return sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM jobs`).Scan(&count) == nil && count == 0
	}, 10*time.Second, 10*time.Millisecond)
	stopSecond()
	require.EqualValues(t, 1, posts.Load(), "restart and retry must never POST a fresh attempt")
	require.EqualValues(t, 2, gets.Load())
	require.False(t, time.Unix(0, terminalGETAt.Load()).Before(nextAvailableAt),
		"retry delivery must respect the persisted Retry-After deadline")
	var deadLetters int
	require.NoError(t, sqlDB.QueryRowContext(ctx, `SELECT count(*) FROM jobs_failed`).Scan(&deadLetters))
	require.Zero(t, deadLetters)
	requireLocalFileBody(t, root, file.GetFullPath(), source)
}

func newAdmissionRestartQueue(t *testing.T, database *hosttest.PgsqlClient, fixture *httptest.Server) *outbox.Service {
	t.Helper()
	jobs := jobsrepo.Must(jobsrepo.NewOptions(database))
	failed := jobsfailedrepo.Must(jobsfailedrepo.NewOptions(database))
	queue, err := outbox.New(outbox.WithJobsRepo(jobs), outbox.WithJobsStatRepo(jobs), outbox.WithJobsFailedRepo(failed),
		outbox.WithTransactor(transaction.New(database.DB())), outbox.WithWorkers(1), outbox.WithIdleTime(100*time.Millisecond),
		outbox.WithReserveFor(time.Minute), outbox.WithLogger(outboxlogger.Default()))
	require.NoError(t, err)
	repo, err := host.NewFileRepo(database, transaction.New(database.DB()))
	require.NoError(t, err)
	// A reconstructed service owns a fresh transport; do not reuse the fixture's
	// already-active client across constructor validation and worker lifetimes.
	client := &http.Client{Transport: &http.Transport{}, Timeout: 5 * time.Second}
	t.Cleanup(client.CloseIdleConnections)
	resizer := clientresizer.Must(clientresizer.NewOptions(client, fixture.URL+"/jobs", fixture.URL+"/jobs", gologger.Discard()))
	cfg := host.StorageConfig{
		Driver: host.StorageDriverLocal, Local: host.StorageLocalConfig{SourceBaseURL: fixture.URL + "/source"},
	}
	resolver, err := host.NewSourceURLResolver(cfg)
	require.NoError(t, err)
	listener := listenresizefile.Must(listenresizefile.NewOptions(repo, queue, eventstream.Discard{}, gologger.Discard()))
	image := host.ImagePipelineConfig{
		ResizerHost: fixture.URL + "/jobs", WebhookCallbackHost: fixture.URL + "/callback",
		Presets: []host.ImagePresetConfig{{Name: "main", Format: "png", Width: 1, Height: 1, Quality: 82}},
	}
	video := host.VideoPipelineConfig{ResizerHost: fixture.URL + "/jobs", WebhookCallbackHost: fixture.URL + "/callback"}
	sender := sendresizefile.Must(sendresizefile.NewOptions(
		repo, resizer, resolver, eventstream.Discard{}, image, video, gologger.Discard(), queue, listener,
	))
	require.NoError(t, queue.RegisterJobs(sendresizefilejob.Must(sendresizefilejob.NewOptions(sender, gologger.Discard()))))
	return queue
}

func runAdmissionRestartQueue(t *testing.T, ctx context.Context, queue *outbox.Service) func() {
	t.Helper()
	runCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- queue.Run(runCtx) }()
	stopped := false
	return func() {
		if stopped {
			return
		}
		stopped = true
		cancel()
		select {
		case err := <-done:
			require.NoError(t, err)
		case <-time.After(5 * time.Second):
			t.Error("outbox worker did not stop within its owned cleanup deadline")
		}
	}
}
