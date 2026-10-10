//go:build measurement

package patchmeasurement_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"runtime/metrics"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	gologger "github.com/assurrussa/gologger"

	"github.com/assurrussa/gouploads/domain/files/service/tusupload"
	"github.com/assurrussa/gouploads/host"
)

const (
	measurementMiB    = 1 << 20
	measurementSize   = 64*measurementMiB + 100*1024
	measurementPeriod = 5 * time.Millisecond
)

//nolint:tagliatelle // The documented measurement schema consistently uses snake_case.
type patchConfig struct {
	ChunkMiB    int `json:"chunk_mib"`
	Concurrency int `json:"concurrency"`
	BudgetMiB   int `json:"admission_budget_mib"`
}

func (c patchConfig) validate() error {
	if c.ChunkMiB != 5 && c.ChunkMiB != 16 && c.ChunkMiB != 32 {
		return errors.New("chunk must be 5, 16 or 32 MiB")
	}
	if c.Concurrency != 1 && c.Concurrency != 4 && c.Concurrency != 16 {
		return errors.New("concurrency must be 1, 4 or 16")
	}
	// This is a conservative admission estimate, not an observed memory bound.
	if c.BudgetMiB < 4*c.ChunkMiB*c.Concurrency || c.BudgetMiB > 8192 {
		return errors.New("admission budget must cover 4*chunk*concurrency and be <=8192 MiB")
	}
	return nil
}

func readPatchConfig() (patchConfig, error) {
	c := patchConfig{ChunkMiB: 5, Concurrency: 1, BudgetMiB: 512}
	for name, target := range map[string]*int{
		"GOUPLOADS_MEASURE_CHUNK_MIB":   &c.ChunkMiB,
		"GOUPLOADS_MEASURE_CONCURRENCY": &c.Concurrency,
		"GOUPLOADS_MEASURE_BUDGET_MIB":  &c.BudgetMiB,
	} {
		if value := os.Getenv(name); value != "" {
			n, err := strconv.Atoi(value)
			if err != nil {
				return c, fmt.Errorf("invalid %s", name)
			}
			*target = n
		}
	}
	return c, c.validate()
}

//nolint:tagliatelle // The documented measurement schema consistently uses snake_case.
type memoryPoint struct {
	RSS          uint64 `json:"rss_bytes"`
	Heap         uint64 `json:"heap_alloc_bytes"`
	LiveAtLastGC uint64 `json:"live_heap_at_last_gc_bytes"`
	TotalAlloc   uint64 `json:"total_alloc_bytes"`
	NumGC        uint32 `json:"num_gc"`
	PauseNS      uint64 `json:"gc_pause_total_ns"`
}

//nolint:tagliatelle // The documented measurement schema consistently uses snake_case.
type serverResult struct {
	Before           memoryPoint `json:"baseline_after_gc"`
	After            memoryPoint `json:"workload_end_before_gc"`
	Retained         memoryPoint `json:"retained_after_gc"`
	PeakRSS          uint64      `json:"sampled_peak_rss_bytes"`
	PeakHeap         uint64      `json:"sampled_peak_heap_alloc_bytes"`
	PeakLiveAtLastGC uint64      `json:"sampled_peak_live_heap_at_last_gc_bytes"`
	Samples          int         `json:"sample_count"`
	VerifiedSessions int         `json:"verified_sessions"`
	VerifiedBytes    int64       `json:"verified_bytes"`
	SHA256           string      `json:"payload_sha256"`
	GoVersion        string      `json:"go_version"`
	GOMAXPROCS       int         `json:"gomaxprocs"`
	GOGC             string      `json:"gogc_env"`
	GOMEMLIMIT       string      `json:"gomemlimit_env"`
}

func snapshotMemory() (memoryPoint, error) {
	var m runtime.MemStats
	runtime.ReadMemStats(&m)
	live := []metrics.Sample{{Name: "/gc/heap/live:bytes"}}
	metrics.Read(live)
	b, err := os.ReadFile("/proc/self/statm")
	if err != nil {
		return memoryPoint{}, err
	}
	fields := strings.Fields(string(b))
	if len(fields) < 2 {
		return memoryPoint{}, errors.New("invalid Linux statm")
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	return memoryPoint{
		RSS: pages * uint64(os.Getpagesize()), Heap: m.HeapAlloc, LiveAtLastGC: live[0].Value.Uint64(),
		TotalAlloc: m.TotalAlloc, NumGC: m.NumGC, PauseNS: m.PauseTotalNs,
	}, err
}

type memorySampler struct {
	stop   chan struct{}
	done   chan struct{}
	result serverResult
	err    error
}

func startMemorySampler() (*memorySampler, error) {
	runtime.GC()
	before, err := snapshotMemory()
	if err != nil {
		return nil, err
	}
	s := &memorySampler{stop: make(chan struct{}), done: make(chan struct{}), result: serverResult{
		Before: before, PeakRSS: before.RSS, PeakHeap: before.Heap, PeakLiveAtLastGC: before.LiveAtLastGC, Samples: 1,
		GoVersion: runtime.Version(), GOMAXPROCS: runtime.GOMAXPROCS(0),
		GOGC: os.Getenv("GOGC"), GOMEMLIMIT: os.Getenv("GOMEMLIMIT"),
	}}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(measurementPeriod)
		defer ticker.Stop()
		for {
			select {
			case <-s.stop:
				return
			case <-ticker.C:
				m, err := snapshotMemory()
				if err != nil {
					s.err = err
					return
				}
				s.result.PeakRSS = max(s.result.PeakRSS, m.RSS)
				s.result.PeakHeap = max(s.result.PeakHeap, m.Heap)
				s.result.PeakLiveAtLastGC = max(s.result.PeakLiveAtLastGC, m.LiveAtLastGC)
				s.result.Samples++
			}
		}
	}()
	return s, nil
}

func (s *memorySampler) finish() (serverResult, error) {
	close(s.stop)
	<-s.done
	if s.err != nil {
		return s.result, s.err
	}
	var err error
	s.result.After, err = snapshotMemory()
	if err != nil {
		return s.result, err
	}
	s.result.PeakRSS = max(s.result.PeakRSS, s.result.After.RSS)
	s.result.PeakHeap = max(s.result.PeakHeap, s.result.After.Heap)
	s.result.PeakLiveAtLastGC = max(s.result.PeakLiveAtLastGC, s.result.After.LiveAtLastGC)
	s.result.Samples++
	runtime.GC()
	s.result.Retained, err = snapshotMemory()
	return s.result, err
}

// ReaderAt lets the client stream each section without retaining a chunk-sized buffer.
type syntheticPDF struct{}

func (syntheticPDF) ReadAt(p []byte, off int64) (int, error) {
	const head, tail = "%PDF-1.7\n", "\n%%EOF\n"
	if off < 0 {
		return 0, errors.New("negative payload offset")
	}
	if off >= measurementSize {
		return 0, io.EOF
	}
	n := min(int64(len(p)), measurementSize-off)
	for i := int64(0); i < n; i++ {
		position := off + i
		switch {
		case position < int64(len(head)):
			p[i] = head[position]
		case position >= measurementSize-int64(len(tail)):
			p[i] = tail[position-(measurementSize-int64(len(tail)))]
		default:
			p[i] = '0'
		}
	}
	if n < int64(len(p)) {
		return int(n), io.EOF
	}
	return int(n), nil
}

type patchLatency struct {
	Kind        string `json:"kind"`
	Bytes       int64  `json:"bytes"`
	Nanoseconds int64  `json:"nanoseconds"`
}

//nolint:tagliatelle // The documented measurement schema consistently uses snake_case.
type latencySummary struct {
	Count int   `json:"count"`
	P50NS int64 `json:"p50_ns"`
	P95NS int64 `json:"p95_ns"`
	P99NS int64 `json:"p99_ns"`
	MaxNS int64 `json:"max_ns"`
}

func summarizeLatency(samples []patchLatency) latencySummary {
	values := make([]int64, len(samples))
	for i, v := range samples {
		values[i] = v.Nanoseconds
	}
	sort.Slice(values, func(i, j int) bool { return values[i] < values[j] })
	if len(values) == 0 {
		return latencySummary{}
	}
	// Nearest-rank quantiles; small sample counts are exposed, not hidden.
	q := func(percent int) int64 { return values[(len(values)*percent+99)/100-1] }
	return latencySummary{Count: len(values), P50NS: q(50), P95NS: q(95), P99NS: q(99), MaxNS: values[len(values)-1]}
}

func patchRequest(ctx context.Context, client *http.Client, method, url string,
	body io.Reader, size int64, headers map[string]string, want int,
) (http.Header, error) {
	req, err := http.NewRequestWithContext(ctx, method, url, body)
	if err != nil {
		return nil, err
	}
	req.ContentLength = size
	req.Header.Set("Tus-Resumable", "1.0.0")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	_, readErr := io.Copy(io.Discard, io.LimitReader(resp.Body, 64*1024))
	closeErr := resp.Body.Close()
	if resp.StatusCode != want {
		return nil, fmt.Errorf("%s returned %d, want %d", method, resp.StatusCode, want)
	}
	return resp.Header, errors.Join(readErr, closeErr)
}

type patchStrategy struct{}

func (patchStrategy) CanUpload(context.Context, host.UploadContext) error { return nil }
func (patchStrategy) GetAfterJobs(context.Context, host.UploadContext) ([]host.FileEventAfterJob, error) {
	return nil, nil
}

func (patchStrategy) GetConfig(context.Context, host.UploadContext) *host.FileUploadConfig {
	return &host.FileUploadConfig{
		UploadDir: "fixture", MaxFileSize: measurementSize,
		AllowedExtensions: []string{".pdf"}, AllowedMimeTypes: map[string][]string{".pdf": {"application/pdf"}},
	}
}

func createPatchSessions(ctx context.Context, client *http.Client, base string, count int) ([]string, error) {
	metadata := make([]string, 0, 5)
	for _, pair := range [][2]string{
		{"filename", "measurement.pdf"},
		{"entity_type", "admin"},
		{"entity_id", "42"},
		{"file_type", "pdf"},
		{"context", "default"},
	} {
		metadata = append(metadata, pair[0]+" "+base64.StdEncoding.EncodeToString([]byte(pair[1])))
	}
	sessions := make([]string, 0, count)
	for range count {
		headers, err := patchRequest(ctx, client, http.MethodPost, base+"/files/tus", nil, 0,
			map[string]string{"Upload-Length": strconv.Itoa(measurementSize), "Upload-Metadata": strings.Join(metadata, ",")},
			http.StatusCreated)
		if err != nil {
			return nil, err
		}
		location := headers.Get("Location")
		id := strings.TrimPrefix(location, "/files/tus/")
		if len(id) != 36 || location != "/files/tus/"+id || strings.ContainsAny(id, "/?#") {
			return nil, errors.New("invalid session location")
		}
		headers, err = patchRequest(ctx, client, http.MethodHead, base+location, nil, 0, nil, http.StatusOK)
		if err != nil {
			return nil, err
		}
		if headers.Get("Upload-Offset") != "0" || headers.Get("Upload-Length") != strconv.Itoa(measurementSize) {
			return nil, errors.New("invalid initial session state")
		}
		sessions = append(sessions, base+location)
	}
	return sessions, nil
}

func uploadPatchSession(ctx context.Context, client *http.Client, url string, chunk int64) ([]patchLatency, error) {
	var samples []patchLatency
	for offset := int64(0); offset < measurementSize; {
		size := min(chunk, measurementSize-offset)
		kind := "steady_full"
		if offset == 0 {
			kind = "first_full"
		} else if size < chunk {
			kind = "final_short"
		}
		started := time.Now()
		headers, err := patchRequest(ctx, client, http.MethodPatch, url, io.NewSectionReader(syntheticPDF{}, offset, size), size,
			map[string]string{"Upload-Offset": strconv.FormatInt(offset, 10), "Content-Type": "application/offset+octet-stream"},
			http.StatusNoContent)
		elapsed := time.Since(started)
		if err != nil {
			return nil, err
		}
		if headers.Get("Upload-Offset") != strconv.FormatInt(offset+size, 10) {
			return nil, errors.New("accepted PATCH returned incorrect offset")
		}
		samples = append(samples, patchLatency{Kind: kind, Bytes: size, Nanoseconds: elapsed.Nanoseconds()})
		offset += size
	}
	return samples, nil
}

func runConcurrentPatches(ctx context.Context, client *http.Client, sessions []string,
	chunk int64,
) ([]patchLatency, time.Duration, error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	type outcome struct {
		samples []patchLatency
		err     error
	}
	results := make(chan outcome, len(sessions))
	start := make(chan struct{})
	var ready sync.WaitGroup
	ready.Add(len(sessions))
	for _, session := range sessions {
		go func(url string) {
			ready.Done()
			<-start
			samples, err := uploadPatchSession(ctx, client, url, chunk)
			if err != nil {
				cancel()
			}
			results <- outcome{samples, err}
		}(session)
	}
	ready.Wait()
	started := time.Now()
	close(start)
	samples := make([]patchLatency, 0, len(sessions)*int((measurementSize+chunk-1)/chunk))
	var resultErr error
	for range sessions {
		r := <-results
		samples = append(samples, r.samples...)
		resultErr = errors.Join(resultErr, r.err)
	}
	return samples, time.Since(started), resultErr
}

func TestMeasurementPatch(t *testing.T) {
	if os.Getenv("GOUPLOADS_MEASURE_PATCH") != "1" {
		t.Skip("opt in with GOUPLOADS_MEASURE_PATCH=1")
	}
	if runtime.GOOS != "linux" {
		t.Skip("RSS sampler currently requires Linux /proc")
	}
	cfg, err := readPatchConfig()
	if err != nil {
		t.Fatal(err)
	}
	if os.Getenv("GOUPLOADS_MEASURE_ROLE") == "server" {
		if err := servePatchMeasurement(t, cfg); err != nil {
			t.Fatal(err)
		}
		return
	}
	if err := runPatchMeasurement(t, cfg); err != nil {
		t.Fatal(err)
	}
}

func runPatchMeasurement(t *testing.T, cfg patchConfig) error {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	executable, err := os.Executable()
	if err != nil {
		return err
	}
	cmd := exec.CommandContext(ctx, executable, "-test.run=^TestMeasurementPatch$", "-test.timeout=8m")
	cmd.Env = append(os.Environ(), "GOUPLOADS_MEASURE_ROLE=server", "GOUPLOADS_MEASURE_PRIVATE_ROOT="+t.TempDir())
	input, err := cmd.StdinPipe()
	if err != nil {
		return err
	}
	defer input.Close()
	output, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		return err
	}
	waited := false
	defer func() {
		if !waited {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
		}
	}()
	encoder, decoder := json.NewEncoder(input), json.NewDecoder(output)
	var ready struct {
		URL string `json:"url"`
	}
	if err := decoder.Decode(&ready); err != nil {
		return fmt.Errorf("server readiness: %w", err)
	}
	if !strings.HasPrefix(ready.URL, "http://127.0.0.1:") {
		return errors.New("fixture must listen on IPv4 loopback")
	}
	transport := &http.Transport{MaxConnsPerHost: cfg.Concurrency, MaxIdleConnsPerHost: cfg.Concurrency}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport, Timeout: time.Minute,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	sessions, err := createPatchSessions(ctx, client, ready.URL, cfg.Concurrency)
	if err != nil {
		return err
	}
	if err := encoder.Encode("start"); err != nil {
		return err
	}
	var ack string
	if err := decoder.Decode(&ack); err != nil {
		return err
	}
	if ack != "started" {
		return errors.New("invalid measurement acknowledgement")
	}
	samples, elapsed, err := runConcurrentPatches(ctx, client, sessions, int64(cfg.ChunkMiB*measurementMiB))
	if err != nil {
		return err
	}
	if err := encoder.Encode("stop"); err != nil {
		return err
	}
	var server serverResult
	if err := decoder.Decode(&server); err != nil {
		return fmt.Errorf("server result/byte verification: %w", err)
	}
	for _, session := range sessions {
		headers, err := patchRequest(ctx, client, http.MethodHead, session, nil, 0, nil, http.StatusOK)
		if err != nil {
			return err
		}
		if headers.Get("Upload-Offset") != strconv.Itoa(measurementSize) ||
			headers.Get("Upload-Length") != strconv.Itoa(measurementSize) {
			return errors.New("HEAD did not verify final session offset/length")
		}
	}
	if err := encoder.Encode("done"); err != nil {
		return err
	}
	if err := cmd.Wait(); err != nil {
		waited = true
		return fmt.Errorf("server process failed: %w (%s)", err, stderr.String())
	}
	waited = true
	if server.VerifiedSessions != cfg.Concurrency || server.VerifiedBytes != int64(cfg.Concurrency)*measurementSize {
		return errors.New("server verification count mismatch")
	}
	return emitPatchResult(t, cfg, server, samples, elapsed)
}

func servePatchMeasurement(t *testing.T, cfg patchConfig) error {
	t.Helper()
	root := os.Getenv("GOUPLOADS_MEASURE_PRIVATE_ROOT")
	if root == "" {
		root = t.TempDir()
	}
	store, err := tusupload.NewFileStore(root)
	if err != nil {
		return err
	}
	user := host.NewUserID()
	handler := host.NewUploadHandler(nil, nil, store, gologger.Discard(),
		func(_ context.Context, metadata map[string]string) (host.UploadContext, error) {
			return host.UploadContext{UserID: 101, UserUUID: user, Metadata: metadata, SessionID: "measurement"}, nil
		}, func(p string) string { return "/files/" + p })
	handler.RegisterStrategy("default", patchStrategy{})
	standard, err := host.NewStandardUploadHandler(handler, "/files",
		host.StandardUploadHandlerConfig{BodyLimit: 32 * measurementMiB})
	if err != nil {
		return err
	}
	// Unused completion/repository routes are unreachable; no queue is constructed.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		isCreate := r.Method == http.MethodPost && r.URL.Path == "/files/tus"
		isSession := strings.HasPrefix(r.URL.Path, "/files/tus/") &&
			!strings.Contains(strings.TrimPrefix(r.URL.Path, "/files/tus/"), "/")
		if !isCreate && (!isSession || (r.Method != http.MethodPatch && r.Method != http.MethodHead)) {
			http.NotFound(w, r)
			return
		}
		standard.ServeHTTP(w, r)
	}))
	defer server.Close()
	encoder, decoder := json.NewEncoder(os.Stdout), json.NewDecoder(os.Stdin)
	if err := encoder.Encode(map[string]string{"url": server.URL}); err != nil {
		return err
	}
	var command string
	if err := decoder.Decode(&command); err != nil {
		return err
	}
	if command != "start" {
		return errors.New("expected start")
	}
	sampler, err := startMemorySampler()
	if err != nil {
		return err
	}
	if err := encoder.Encode("started"); err != nil {
		_, _ = sampler.finish()
		return err
	}
	if err := decoder.Decode(&command); err != nil {
		_, _ = sampler.finish()
		return err
	}
	result, err := sampler.finish()
	if err != nil {
		return err
	}
	if command != "stop" {
		return errors.New("expected stop")
	}
	// Verification follows all memory snapshots, including retained heap.
	if err := verifyPatchFiles(t.Context(), store, root, cfg.Concurrency, &result); err != nil {
		return err
	}
	if err := encoder.Encode(result); err != nil {
		return err
	}
	if err := decoder.Decode(&command); err != nil {
		return err
	}
	if command != "done" {
		return errors.New("expected done")
	}
	return nil
}

func emitPatchResult(t *testing.T, cfg patchConfig, server serverResult, samples []patchLatency, elapsed time.Duration) error {
	t.Helper()
	groups := map[string]latencySummary{"all": summarizeLatency(samples)}
	for _, kind := range []string{"first_full", "steady_full", "final_short"} {
		var selected []patchLatency
		for _, sample := range samples {
			if sample.Kind == kind {
				selected = append(selected, sample)
			}
		}
		groups[kind] = summarizeLatency(selected)
	}
	cgroupLimit := "unavailable"
	if value, err := os.ReadFile("/sys/fs/cgroup/memory.max"); err == nil {
		cgroupLimit = strings.TrimSpace(string(value))
	}
	allocatedPerPatch := float64(server.After.TotalAlloc-server.Before.TotalAlloc) / float64(len(samples))
	result := map[string]any{
		"schema": "gouploads.patch-measurement.v1", "status": "MEASURED",
		"profile": "local_file_store", "adapter": "standard_net_http", "buffers": "fresh_process_no_warmup",
		"config": cfg, "bytes_per_upload": measurementSize, "body_limit_bytes": 32 * measurementMiB,
		"sample_period_ns": measurementPeriod.Nanoseconds(), "connection_reuse": true,
		"cgroup_v2_memory_max": cgroupLimit, "server": server,
		"allocated_bytes_per_patch_including_observer": allocatedPerPatch,
		"workload_gc_cycles":                           server.After.NumGC - server.Before.NumGC,
		"workload_gc_pause_ns":                         server.After.PauseNS - server.Before.PauseNS,
		"client_wall_ns":                               elapsed.Nanoseconds(),
		"throughput_bytes_per_second":                  float64(server.VerifiedBytes) / elapsed.Seconds(),
		"client_patch_latency":                         groups, "client_patch_samples": samples,
		"correctness":                           "all PATCH 204, offsets, HEAD length, active sessions and stored SHA256 verified",
		"complete_and_background_jobs_excluded": true,
	}
	b, err := json.Marshal(result)
	if err != nil {
		return err
	}
	t.Logf("GOUPLOADS_PATCH_MEASUREMENT %s", b)
	return nil
}

func TestPatchFixtureConfig(t *testing.T) {
	for _, chunk := range []int{5, 16, 32} {
		for _, concurrency := range []int{1, 4, 16} {
			if err := (patchConfig{chunk, concurrency, 2048}).validate(); err != nil {
				t.Fatal(err)
			}
		}
	}
	for _, cfg := range []patchConfig{{4, 1, 512}, {5, 2, 512}, {32, 16, 512}, {5, 1, 9000}, {5, 1, -1}} {
		if cfg.validate() == nil {
			t.Fatalf("accepted invalid config: %+v", cfg)
		}
	}
}

func TestPatchFixtureSyntheticPDF(t *testing.T) {
	for _, tc := range []struct {
		offset int64
		want   string
	}{{0, "%PDF-1.7\n000"}, {measurementSize - 9, "00\n%%EOF\n"}, {16, "0000"}} {
		buffer := make([]byte, len(tc.want))
		n, err := (syntheticPDF{}).ReadAt(buffer, tc.offset)
		if err != nil || n != len(buffer) || string(buffer) != tc.want {
			t.Fatalf("offset %d: n=%d, err=%v, bytes=%q", tc.offset, n, err, buffer)
		}
	}
	b := make([]byte, 2)
	if n, err := (syntheticPDF{}).ReadAt(b, measurementSize-1); n != 1 || !errors.Is(err, io.EOF) {
		t.Fatalf("partial read: %d, %v", n, err)
	}
	if _, err := (syntheticPDF{}).ReadAt(b, -1); err == nil {
		t.Fatal("negative offset accepted")
	}
}

func TestPatchFixtureLatencyQuantiles(t *testing.T) {
	got := summarizeLatency([]patchLatency{{Nanoseconds: 4}, {Nanoseconds: 1}, {Nanoseconds: 3}, {Nanoseconds: 2}})
	if got.Count != 4 || got.P50NS != 2 || got.P95NS != 4 || got.P99NS != 4 || got.MaxNS != 4 {
		t.Fatalf("incorrect nearest-rank summary: %+v", got)
	}
	if summarizeLatency(nil) != (latencySummary{}) {
		t.Fatal("empty summary is not empty")
	}
}

func TestPatchFixtureRejectsRejectedOrWrongOffset(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusNoContent} {
		t.Run(strconv.Itoa(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				w.Header().Set("Upload-Offset", "0")
				w.WriteHeader(status)
			}))
			defer server.Close()
			// The first 1 KiB response is enough to prove rejected traffic cannot
			// enter successful latency samples; this is not a throughput sample.
			samples, err := uploadPatchSession(t.Context(), server.Client(), server.URL, 1024)
			if err == nil || len(samples) != 0 {
				t.Fatalf("invalid response entered measurements: %v, %d", err, len(samples))
			}
		})
	}
}

func verifyPatchFiles(ctx context.Context, store *tusupload.FileStore, root string, want int, result *serverResult) error {
	wantHash := sha256.New()
	if _, err := io.Copy(wantHash, io.NewSectionReader(syntheticPDF{}, 0, measurementSize)); err != nil {
		return err
	}
	result.SHA256 = hex.EncodeToString(wantHash.Sum(nil))
	entries, err := os.ReadDir(root)
	if err != nil {
		return err
	}
	for _, entry := range entries {
		if entry.Name() == ".locks" {
			continue
		}
		if !entry.IsDir() {
			return errors.New("unexpected fixture file")
		}
		session, err := store.Get(ctx, entry.Name())
		if err != nil {
			return err
		}
		if session.Offset != measurementSize || session.UploadLength != measurementSize || session.Status != tusupload.StatusActive {
			return errors.New("session did not retain the complete accepted PATCH bytes in active state")
		}
		f, err := os.OpenInRoot(root, filepath.Join(entry.Name(), "data"))
		if err != nil {
			return err
		}
		h := sha256.New()
		n, copyErr := io.Copy(h, f)
		closeErr := f.Close()
		if err := errors.Join(copyErr, closeErr); err != nil {
			return err
		}
		if n != measurementSize || hex.EncodeToString(h.Sum(nil)) != result.SHA256 {
			return errors.New("stored byte checksum mismatch")
		}
		result.VerifiedSessions++
		result.VerifiedBytes += n
	}
	if result.VerifiedSessions != want {
		return errors.New("unexpected valid session count")
	}
	return nil
}
