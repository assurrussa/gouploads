//go:build integration

package originals_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/assurrussa/outbox/backends/pgsql/migrations"
	"github.com/assurrussa/outbox/backends/pgsql/storage"
	"github.com/assurrussa/outbox/backends/pgsql/storage/transaction"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/require"

	"github.com/assurrussa/gouploads/host"
	"github.com/assurrussa/gouploads/hosttest"
)

const (
	lockProbePoll = 5 * time.Millisecond
	lockProbeHold = 25 * time.Millisecond
)

// TestIntegrationOriginalFinalizerLockMeasurement is an opt-in, single-worker
// diagnostic, not a throughput benchmark. Both cases pause local SavePersist
// until a separate PostgreSQL transaction is observed waiting on this finalizer.
// The pause, observer overhead and scheduling are INCLUDED in the result.
// No S3 request or S3Store.Complete lease is measured here.
func TestIntegrationOriginalFinalizerLockMeasurement(t *testing.T) {
	if os.Getenv("GOUPLOADS_MEASURE_FINALIZER_LOCK") != "1" {
		t.Skip("set GOUPLOADS_MEASURE_FINALIZER_LOCK=1 for the owned PostgreSQL fixture")
	}
	mib := 1
	if text := os.Getenv("GOUPLOADS_MEASURE_FINALIZER_MIB"); text != "" {
		var err error
		mib, err = strconv.Atoi(text)
		require.NoError(t, err)
	}
	require.Contains(t, []int{1, 16, 128}, mib, "supported source sizes in MiB")
	for _, scenario := range []string{"normal_gated", "cancel_gated"} {
		t.Run(scenario, func(t *testing.T) {
			measureOriginalFinalizerLock(t, scenario, mib<<20)
		})
	}
}

type lockProbeContextKey struct{}

//nolint:tagliatelle // The documented measurement schema consistently uses snake_case.
type lockProbeEvent struct {
	Phase    string `json:"phase"`
	OffsetNS int64  `json:"offset_ns"`
	Outcome  string `json:"outcome,omitempty"`
}

type lockProbe struct {
	start                        time.Time
	mu                           sync.Mutex
	log                          []lockProbeEvent
	pid                          atomic.Uint32
	readNS, readBytes, readCalls atomic.Int64
}

func newLockProbe() *lockProbe { return &lockProbe{start: time.Now()} }

func probeFrom(ctx context.Context) *lockProbe {
	p, _ := ctx.Value(lockProbeContextKey{}).(*lockProbe)
	return p
}

func (p *lockProbe) mark(phase, outcome string) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.log = append(p.log, lockProbeEvent{Phase: phase, OffsetNS: time.Since(p.start).Nanoseconds(), Outcome: outcome})
}

func (p *lockProbe) events() []lockProbeEvent {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]lockProbeEvent(nil), p.log...)
}

func lockProbeOutcome(err error) string {
	switch {
	case err == nil:
		return "ok"
	case errors.Is(err, context.Canceled):
		return "canceled"
	case errors.Is(err, context.DeadlineExceeded):
		return "deadline"
	default:
		return "error"
	}
}

type lockProbeClient struct {
	storage.Client
	engine storage.DBEngine
}

func (c *lockProbeClient) DB() storage.DBEngine { return c.engine }

type lockProbeDB struct{ storage.DBEngine }

func (db *lockProbeDB) BeginTx(ctx context.Context, options pgx.TxOptions) (pgx.Tx, error) {
	p := probeFrom(ctx)
	if p == nil {
		return db.DBEngine.BeginTx(ctx, options)
	}
	p.mark("begin_call", "")
	tx, err := db.DBEngine.BeginTx(ctx, options)
	p.mark("begin_return", lockProbeOutcome(err))
	if err != nil {
		return tx, err
	}
	p.pid.Store(tx.Conn().PgConn().PID())
	return &lockProbeTx{Tx: tx, probe: p}, nil
}

// Intercept the repository's scan, not QueryRow's early return. Forward the
// supplied context unchanged so storage.GetTx still reaches the same pgx.Tx.
func (db *lockProbeDB) ScanOnex(ctx context.Context, op string, dest any, query storage.Sqlizer) error {
	p := probeFrom(ctx)
	if p == nil || op != "filerepo.GetByIDForUpdate" {
		return db.DBEngine.ScanOnex(ctx, op, dest, query)
	}
	p.mark("for_update_call", "")
	err := db.DBEngine.ScanOnex(ctx, op, dest, query)
	p.mark("for_update_return", lockProbeOutcome(err))
	return err
}

type lockProbeTx struct {
	pgx.Tx
	probe *lockProbe
}

func (tx *lockProbeTx) Commit(ctx context.Context) error {
	tx.probe.mark("commit_call", "")
	err := tx.Tx.Commit(ctx)
	tx.probe.mark("commit_return", lockProbeOutcome(err))
	return err
}

func (tx *lockProbeTx) Rollback(ctx context.Context) error {
	tx.probe.mark("rollback_call", "")
	err := tx.Tx.Rollback(ctx)
	tx.probe.mark("rollback_return", lockProbeOutcome(err))
	return err
}

type lockProbeStorage struct {
	host.Storage
	entered chan struct{}
	release chan struct{}
}

func (s *lockProbeStorage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	p := probeFrom(ctx)
	if p == nil {
		return s.Storage.Open(ctx, key)
	}
	p.mark("storage_open_call", "")
	reader, err := s.Storage.Open(ctx, key)
	p.mark("storage_open_return", lockProbeOutcome(err))
	if err != nil || reader == nil {
		return reader, err
	}
	return &lockProbeReader{ReadCloser: reader, probe: p}, nil
}

func (s *lockProbeStorage) SavePersist(ctx context.Context, input host.SaveFileInput) (stored host.StoredFile, err error) {
	p := probeFrom(ctx)
	if p == nil {
		return s.Storage.SavePersist(ctx, input)
	}
	p.mark("storage_save_call", "")
	defer func() { p.mark("storage_save_return", lockProbeOutcome(err)) }()
	p.mark("storage_gate_enter", "")
	close(s.entered)
	select {
	case <-s.release:
	case <-ctx.Done():
	}
	err = ctx.Err()
	p.mark("storage_gate_return", lockProbeOutcome(err))
	if err != nil {
		return host.StoredFile{}, err
	}
	return s.Storage.SavePersist(ctx, input)
}

type lockProbeReader struct {
	io.ReadCloser
	probe *lockProbe
}

func (r *lockProbeReader) Read(data []byte) (int, error) {
	start := time.Now()
	n, err := r.ReadCloser.Read(data)
	r.probe.readNS.Add(time.Since(start).Nanoseconds())
	r.probe.readBytes.Add(int64(n))
	r.probe.readCalls.Add(1)
	return n, err
}

func measureOriginalFinalizerLock(t *testing.T, scenario string, size int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	t.Cleanup(cancel)
	db, _, cleanup := hosttest.PrepareDB(ctx, t, "original_lock_measure",
		hosttest.WithDatabaseLog(func(string, ...any) {}))
	t.Cleanup(func() {
		cleanupCtx, stop := context.WithTimeout(context.Background(), 10*time.Second)
		defer stop()
		cleanup(cleanupCtx)
	})
	pool := db.DB().Pool()
	require.GreaterOrEqual(t, pool.Config().MaxConns, int32(3), "finalizer, waiter and observer need independent connections")
	sqlDB := stdlib.OpenDBFromPool(pool)
	provider, err := goose.NewProvider(goose.DialectPostgres, sqlDB, migrations.FS, goose.WithTableName("outbox_schema_versions"))
	require.NoError(t, err)
	_, err = provider.Up(ctx)
	require.NoError(t, err)
	require.NoError(t, sqlDB.Close())
	cfg := storageConfig(t, ctx, host.StorageDriverLocal)
	base, err := host.NewStorage(cfg)
	require.NoError(t, err)
	store := &lockProbeStorage{Storage: base, entered: make(chan struct{}), release: make(chan struct{})}
	engine := &lockProbeDB{DBEngine: db.DB()}
	client := &lockProbeClient{Client: db, engine: engine}
	events := &originalCancelEvents{}
	queue := newQueue(t, db)
	uploads, err := host.NewOriginalRuntime(cfg, host.OriginalRuntimeDeps{
		Database: client, Transaction: transaction.New(engine), Outbox: queue,
		Storage: store, Events: events,
	})
	require.NoError(t, err)
	body := bytes.Repeat([]byte{'x'}, size)
	copy(body, "%PDF-1.7\n")
	copy(body[len(body)-7:], "\n%%EOF\n")
	uploadCfg := uploadConfig()
	uploadCfg.MaxFileSize = int64(size)
	user := host.NewUserID()
	file, err := uploads.Uploader.UploadReader(ctx, host.ReaderRequest{
		UploaderUUID: user, UserID: 101, ObjectType: host.ObjectTypeAdmin, ObjectID: 42, Config: uploadCfg,
		AfterJobs: host.NewFileEventAfterJobs("after_original", user),
	}, host.ReaderUploadInput{OriginalName: "lock-measure.pdf", Size: int64(size), Reader: bytes.NewReader(body)})
	require.NoError(t, err)
	initialJobs := lockProbeJobCount(t, ctx, pool)
	initialEvents := events.count.Load()

	// Everything above is setup. Only this HandleOriginal call receives a probe;
	// retries, correctness reads, migration and cleanup are outside its timeline.
	p := newLockProbe()
	uploadCtx, stopUpload := context.WithCancel(context.WithValue(ctx, lockProbeContextKey{}, p))
	finalized := make(chan error, 1)
	finalizerStopped := make(chan struct{})
	go func() {
		defer close(finalizerStopped)
		err := uploads.Finalizer.HandleOriginal(uploadCtx, file.ID)
		p.mark("finalizer_return", lockProbeOutcome(err))
		finalized <- err
	}()
	t.Cleanup(func() { stopUpload(); lockProbeJoin(t, finalizerStopped) })
	select {
	case <-store.entered:
	case err = <-finalized:
		t.Fatalf("finalizer returned before storage gate: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}

	locked := make(chan error, 1)
	waiterPID := make(chan uint32, 1)
	waiterStopped := make(chan struct{})
	waitCtx, stopWait := context.WithCancel(ctx)
	go func() {
		defer close(waiterStopped)
		locked <- lockProbeWaiter(waitCtx, pool, file.ID, p, waiterPID)
	}()
	t.Cleanup(func() { stopWait(); lockProbeJoin(t, waiterStopped) })
	var pid uint32
	select {
	case pid = <-waiterPID:
	case err = <-locked:
		t.Fatalf("waiter failed before its locking query: %v", err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	require.NotEqual(t, p.pid.Load(), pid, "waiter must use an independent PostgreSQL connection")
	lockProbeObserveBlock(t, ctx, pool, pid, p.pid.Load())
	p.mark("waiter_block_observed", "ok")
	select {
	case err = <-locked:
		t.Fatalf("waiter escaped while the storage gate held the row: %v", err)
	default:
	}
	timer := time.NewTimer(lockProbeHold)
	defer timer.Stop()
	select {
	case <-timer.C:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if scenario == "cancel_gated" {
		p.mark("cancel_requested", "")
		stopUpload()
	} else {
		p.mark("gate_release_requested", "")
		close(store.release)
	}
	select {
	case err = <-finalized:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	if scenario == "cancel_gated" {
		require.ErrorIs(t, err, context.Canceled)
	} else {
		require.NoError(t, err)
	}
	select {
	case err = <-locked:
		require.NoError(t, err, "waiter's completed scan proves the row lock was released")
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	lockProbeCheckResult(t, ctx, pool, uploads, file, body, scenario, events, initialEvents, initialJobs)
	lockProbeReport(t, p, scenario, size, pool.Config().MaxConns)
}

func lockProbeJoin(t *testing.T, stopped <-chan struct{}) {
	t.Helper()
	select {
	case <-stopped:
	case <-time.After(5 * time.Second):
		t.Error("measurement goroutine did not stop after cancellation")
	}
}

func lockProbeWaiter(ctx context.Context, pool *pgxpool.Pool, fileID int64, p *lockProbe, ready chan<- uint32) (err error) {
	p.mark("waiter_begin_call", "")
	tx, err := pool.BeginTx(ctx, pgx.TxOptions{})
	p.mark("waiter_begin_return", lockProbeOutcome(err))
	if err != nil {
		return err
	}
	defer func() {
		// Only the fixture waiter's cleanup is detached. The measured transaction
		// wrappers never replace the finalizer's commit or rollback context.
		cleanupCtx, stop := context.WithTimeout(context.WithoutCancel(ctx), 2*time.Second)
		defer stop()
		err = errors.Join(err, tx.Rollback(cleanupCtx))
	}()
	ready <- tx.Conn().PgConn().PID()
	p.mark("waiter_for_update_call", "")
	var got int64
	err = tx.QueryRow(ctx, "SELECT id FROM files WHERE id=$1 FOR UPDATE", fileID).Scan(&got)
	if err == nil && got != fileID {
		err = errors.New("waiter acquired an unexpected fixture row")
	}
	p.mark("waiter_for_update_return", lockProbeOutcome(err))
	return err
}

func lockProbeObserveBlock(t *testing.T, ctx context.Context, pool *pgxpool.Pool, waiter, blocker uint32) {
	t.Helper()
	observeCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	ticker := time.NewTicker(lockProbePoll)
	defer ticker.Stop()
	for {
		var blocked bool
		err := pool.QueryRow(observeCtx, `SELECT EXISTS (
SELECT 1 FROM pg_stat_activity
WHERE datname=current_database() AND pid=$1 AND wait_event_type='Lock'
AND $2 = ANY(pg_blocking_pids(pid)))`, int32(waiter), int32(blocker)).Scan(&blocked)
		require.NoError(t, err)
		if blocked {
			return
		}
		select {
		case <-ticker.C:
		case <-observeCtx.Done():
			t.Fatal("owned waiter was not observed blocked by the measured finalizer")
		}
	}
}

type lockProbeJobs struct {
	total, afterOriginal, deletedFile int
}

func lockProbeJobCount(t *testing.T, ctx context.Context, pool *pgxpool.Pool) lockProbeJobs {
	t.Helper()
	var count lockProbeJobs
	require.NoError(t, pool.QueryRow(ctx, `SELECT count(*),
count(*) FILTER (WHERE name='after_original'),
count(*) FILTER (WHERE name='deleted_file') FROM jobs`).Scan(&count.total, &count.afterOriginal, &count.deletedFile))
	return count
}

func lockProbeCheckResult(t *testing.T, ctx context.Context, pool *pgxpool.Pool, uploads *host.OriginalRuntime,
	queued host.File, body []byte, scenario string, events *originalCancelEvents, initialEvents int64, initialJobs lockProbeJobs,
) {
	t.Helper()
	if scenario == "cancel_gated" {
		retained, err := uploads.Files.GetByID(ctx, queued.ID)
		require.NoError(t, err)
		require.Equal(t, queued.GetData(), retained.GetData(), "rollback retains retry metadata")
		require.Equal(t, initialJobs, lockProbeJobCount(t, ctx, pool), "rollback adds no completion jobs")
		require.Equal(t, initialEvents, events.count.Load(), "rollback publishes no event")
		requireBytes(t, ctx, uploads.Storage, queued.GetFullPath(), body)
		// Retry without the measurement context or artificial gate.
		require.NoError(t, uploads.Finalizer.HandleOriginal(ctx, queued.ID))
	}
	file, err := uploads.Files.GetByID(ctx, queued.ID)
	require.NoError(t, err)
	require.Empty(t, file.GetData().Uploader)
	require.Len(t, file.GetData().Presets, 1)
	sum := sha256.Sum256(body)
	preset := file.GetData().Presets[host.PresetName("main")]
	require.Equal(t, hex.EncodeToString(sum[:]), preset.ChecksumSHA256)
	require.EqualValues(t, len(body), file.Size)
	requireBytes(t, ctx, uploads.Storage, file.GetFullPath(), body)
	wantJobs := initialJobs
	wantJobs.total += 2
	wantJobs.afterOriginal++
	wantJobs.deletedFile++
	require.Equal(t, wantJobs, lockProbeJobCount(t, ctx, pool), "exactly one after-job and one staging-cleanup job")
	require.Equal(t, initialEvents+1, events.count.Load())
	// The direct handler does not reserve/delete the original queue row. Repeated
	// delivery must not duplicate completion jobs or events.
	require.NoError(t, uploads.Finalizer.HandleOriginal(ctx, queued.ID))
	require.Equal(t, wantJobs, lockProbeJobCount(t, ctx, pool))
	require.Equal(t, initialEvents+1, events.count.Load())
}

//nolint:tagliatelle // The documented measurement result struct consistently uses snake_case.
func lockProbeReport(t *testing.T, p *lockProbe, scenario string, size int, poolMax int32) {
	t.Helper()
	log := p.events()
	points := make(map[string]lockProbeEvent, len(log))
	var previous int64
	for _, event := range log {
		require.NotContains(t, points, event.Phase)
		require.GreaterOrEqual(t, event.OffsetNS, previous)
		points[event.Phase] = event
		previous = event.OffsetNS
	}
	terminal := "commit_return"
	if scenario == "cancel_gated" {
		terminal = "rollback_return"
		require.NotContains(t, points, "commit_call")
	} else {
		require.Equal(t, "ok", points[terminal].Outcome)
		require.EqualValues(t, size, p.readBytes.Load())
	}
	for _, phase := range []string{"begin_return", "for_update_return", "storage_open_return", "waiter_for_update_return"} {
		require.Equal(t, "ok", points[phase].Outcome, phase)
	}
	interval := func(first, last string) int64 {
		t.Helper()
		require.Contains(t, points, first)
		require.Contains(t, points, last)
		ns := points[last].OffsetNS - points[first].OffsetNS
		require.GreaterOrEqual(t, ns, int64(0), first+" to "+last)
		return ns
	}
	// A waiter can receive its response BEFORE Commit/Rollback returns to this
	// process. Do not impose client-response ordering or call this exact lock hold.
	require.Greater(t, points["waiter_for_update_return"].OffsetNS, points["waiter_block_observed"].OffsetNS)
	result := struct {
		Schema                int              `json:"schema"`
		Scope                 string           `json:"scope"`
		Scenario              string           `json:"scenario"`
		Storage               string           `json:"storage"`
		SourceBytes           int              `json:"source_bytes"`
		GoVersion             string           `json:"go_version"`
		GOMAXPROCS            int              `json:"gomaxprocs"`
		PoolMaxConns          int32            `json:"pool_max_conns"`
		WorkerConcurrency     int              `json:"worker_concurrency"`
		PollIntervalNS        int64            `json:"poll_interval_ns"`
		InjectedMinimumHoldNS int64            `json:"injected_minimum_hold_ns"`
		BlockingVerified      bool             `json:"blocking_verified"`
		CorrectnessVerified   bool             `json:"correctness_verified"`
		TerminalOutcome       string           `json:"terminal_outcome"`
		SourceReadCalls       int64            `json:"source_read_calls"`
		SourceReadBytes       int64            `json:"source_read_bytes"`
		SourceReadCallTotalNS int64            `json:"source_read_call_total_ns"`
		IntervalsNS           map[string]int64 `json:"intervals_ns"`
		Events                []lockProbeEvent `json:"events"`
	}{
		Schema: 1, Scope: "original_finalizer_sql_row_lock", Scenario: scenario,
		Storage: "local", SourceBytes: size, GoVersion: runtime.Version(),
		GOMAXPROCS: runtime.GOMAXPROCS(0), PoolMaxConns: poolMax, WorkerConcurrency: 1,
		PollIntervalNS: int64(lockProbePoll), InjectedMinimumHoldNS: int64(lockProbeHold),
		BlockingVerified: true, CorrectnessVerified: true, TerminalOutcome: points[terminal].Outcome,
		SourceReadCalls: p.readCalls.Load(), SourceReadBytes: p.readBytes.Load(), SourceReadCallTotalNS: p.readNS.Load(),
		IntervalsNS: map[string]int64{
			"begin_call":                  interval("begin_call", "begin_return"),
			"locking_query":               interval("for_update_call", "for_update_return"),
			"client_transaction":          interval("begin_return", terminal),
			"post_acquisition_client":     interval("for_update_return", terminal),
			"waiter_query":                interval("waiter_for_update_call", "waiter_for_update_return"),
			"storage_open":                interval("storage_open_call", "storage_open_return"),
			"storage_save_including_gate": interval("storage_save_call", "storage_save_return"),
			"storage_gate":                interval("storage_gate_enter", "storage_gate_return"),
		}, Events: log,
	}
	encoded, err := json.Marshal(result)
	require.NoError(t, err)
	t.Logf("GOUPLOADS_FINALIZER_LOCK %s", encoded)
}

// These contract tests need no PostgreSQL service or opt-in environment variable.
func TestFinalizerLockProbeScanContextAndCompletion(t *testing.T) {
	p := newLockProbe()
	tx := &lockProbeFakeTx{}
	ctx := storage.WithTx(context.WithValue(context.Background(), lockProbeContextKey{}, p), tx)
	want := errors.New("fixture private query failure")
	db := &lockProbeDB{DBEngine: &lockProbeFakeDB{scan: func(got context.Context, _ string, _ any, _ storage.Sqlizer) error {
		require.Same(t, ctx, got)
		require.Same(t, tx, storage.GetTx(got))
		log := p.events()
		require.Len(t, log, 1)
		require.Equal(t, "for_update_call", log[0].Phase)
		return want
	}}}
	require.ErrorIs(t, db.ScanOnex(ctx, "filerepo.GetByIDForUpdate", nil, nil), want)
	log := p.events()
	require.Len(t, log, 2)
	require.Equal(t, "for_update_return", log[1].Phase)
	require.Equal(t, "error", log[1].Outcome)
	encoded, err := json.Marshal(log)
	require.NoError(t, err)
	require.NotContains(t, string(encoded), want.Error(), "never emit error details or SQL arguments")
}

type lockProbeFakeTx struct {
	pgx.Tx
	commit, rollback func(context.Context) error
}

func TestFinalizerLockProbeTransactionContextAndCompletion(t *testing.T) {
	for _, terminal := range []string{"commit", "rollback"} {
		t.Run(terminal, func(t *testing.T) {
			p := newLockProbe()
			ctx, cancel := context.WithCancel(context.WithValue(context.Background(), lockProbeContextKey{}, p))
			defer cancel()
			if terminal == "rollback" {
				cancel()
			}
			complete := func(got context.Context) error {
				require.Same(t, ctx, got)
				require.Same(t, p, probeFrom(got))
				require.ErrorIs(t, got.Err(), ctx.Err(), "instrumentation preserves cancellation")
				log := p.events()
				require.Len(t, log, 1)
				require.Equal(t, terminal+"_call", log[0].Phase)
				return nil
			}
			tx := &lockProbeTx{Tx: &lockProbeFakeTx{commit: complete, rollback: complete}, probe: p}
			var err error
			if terminal == "commit" {
				err = tx.Commit(ctx)
			} else {
				err = tx.Rollback(ctx)
			}
			require.NoError(t, err)
			log := p.events()
			require.Len(t, log, 2)
			require.Equal(t, terminal+"_return", log[1].Phase)
			require.Equal(t, "ok", log[1].Outcome)
		})
	}
}

func (tx *lockProbeFakeTx) Commit(ctx context.Context) error   { return tx.commit(ctx) }
func (tx *lockProbeFakeTx) Rollback(ctx context.Context) error { return tx.rollback(ctx) }

type lockProbeFakeDB struct {
	storage.DBEngine
	scan func(context.Context, string, any, storage.Sqlizer) error
}

func (db *lockProbeFakeDB) ScanOnex(ctx context.Context, op string, dest any, query storage.Sqlizer) error {
	return db.scan(ctx, op, dest, query)
}

type lockProbeFakeStorage struct {
	host.Storage
	open func(context.Context, string) (io.ReadCloser, error)
	save func(context.Context, host.SaveFileInput) (host.StoredFile, error)
}

func (s *lockProbeFakeStorage) Open(ctx context.Context, key string) (io.ReadCloser, error) {
	return s.open(ctx, key)
}

func (s *lockProbeFakeStorage) SavePersist(ctx context.Context, input host.SaveFileInput) (host.StoredFile, error) {
	return s.save(ctx, input)
}

func TestFinalizerLockProbeStorageContextAndReadAccounting(t *testing.T) {
	p := newLockProbe()
	ctx := context.WithValue(context.Background(), lockProbeContextKey{}, p)
	base := &lockProbeFakeStorage{open: func(got context.Context, _ string) (io.ReadCloser, error) {
		require.Same(t, ctx, got)
		return io.NopCloser(bytes.NewReader([]byte("fixture"))), nil
	}}
	s := &lockProbeStorage{Storage: base}
	reader, err := s.Open(ctx, "private-fixture-key")
	require.NoError(t, err)
	data, err := io.ReadAll(reader)
	require.NoError(t, err)
	require.NoError(t, reader.Close())
	require.Equal(t, "fixture", string(data))
	require.EqualValues(t, len(data), p.readBytes.Load())
	require.Positive(t, p.readCalls.Load())
}

func TestFinalizerLockProbeStorageGateCancellation(t *testing.T) {
	p := newLockProbe()
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), lockProbeContextKey{}, p))
	cancel()
	base := &lockProbeFakeStorage{save: func(context.Context, host.SaveFileInput) (host.StoredFile, error) {
		t.Fatal("cancellation must not write after the artificial gate")
		return host.StoredFile{}, nil
	}}
	s := &lockProbeStorage{Storage: base, entered: make(chan struct{}), release: make(chan struct{})}
	_, err := s.SavePersist(ctx, host.SaveFileInput{})
	require.ErrorIs(t, err, context.Canceled)
	log := p.events()
	require.Equal(t, "storage_save_return", log[len(log)-1].Phase)
	require.Equal(t, "canceled", log[len(log)-1].Outcome)
}
