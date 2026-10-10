# Opt-in PATCH memory and finalizer lock fixtures

Candidate source, 2026-10-10. Compile and runtime status: **NOT_RUN**. These
fixtures do not establish a bottleneck, an improvement, or new production
defaults. They add no production API, metric dependency, CI trigger, S3 setup,
credentials, or worker startup.

## Candidate contents and baseline

- `integration/patchmeasurement/patch_measurement_test.go`: build tag
  `measurement`; local FileStore through the production standard HTTP adapter.
- `integration/originals/finalizer_lock_measurement_integration_test.go`: build
  tag `integration`; existing original finalizer with test-only wrappers.
- `docs/measurement-fixtures.md`: this guide.

The inspected checkout is commit `7e7f50e618481a06d637cd1ff6228f9a09969513`, tree
`b0a65cfa847545bf1035035b916aeba94c8431aa`. The preceding verified source plan
recorded the same tree for upstream `b35f1596adb21a47e14d7eed97103210238d7977`;
that upstream commit object is absent locally and was not independently fetched
again. Candidate files are isolated additions. Existing dirty backlog/example
files and branch state remain untouched.

The attempted Go compilation was blocked before compilation:
`go: creating work dir: mkdir /tmp/go-build2882561748: read-only file system`.
No writable-path workaround, external executor, database, upload service or
measurement process was launched. A successful gofmt/source review is not a
compile or test pass.

## PATCH: exact manual invocation

Apply the reviewed candidate into an authorized writable checkout first. Use
Go 1.27.2 and existing permitted shared module/build caches. Do not create a
per-checkout cache. These commands are future invocations, not execution evidence.

```sh
export GOTOOLCHAIN=local
GO=/path/to/go1.27.2/bin/go

# Helper tests only; fake HTTP responses, no real upload service or subprocess.
"$GO" test -tags measurement ./integration/patchmeasurement \
  -run '^TestPatchFixture' -count=1

# Start with the smallest point and inspect memory/disk capacity before scaling.
GOUPLOADS_MEASURE_PATCH=1 GOUPLOADS_MEASURE_CHUNK_MIB=5 \
GOUPLOADS_MEASURE_CONCURRENCY=1 GOUPLOADS_MEASURE_BUDGET_MIB=512 \
GOGC=100 GOMEMLIMIT=512MiB \
"$GO" test -tags measurement ./integration/patchmeasurement \
  -run '^TestMeasurementPatch$' -count=1 -timeout=10m -v
```

Repeat a chosen point in fresh invocations for repeatability. The complete
supported matrix is chunk MiB `{5,16,32}` by concurrency `{1,4,16}`. The budget
must be at least `4 * chunkMiB * concurrency` and no more than 8192 MiB; the
largest point therefore requires at least 2048 MiB admission budget. This is an
explicit conservative estimate, not a hard memory limit or a measured multiplier.
`GOMEMLIMIT` is Go's soft limit; use an owned container limit if a hard cap is
needed and record it. Scaling is a deliberate manual decision, not automatic.

Every invocation launches exactly one loopback-only server child process using
the same test executable. The parent is the separate streaming HTTP client;
the parent owns the temporary storage root and cleans it even after child
failure. Server startup, POST session creation, initial HEAD and baseline GC
precede the sample window. The fixed upload size is 64 MiB + 100 KiB per
session; maximum temporary data is roughly 1 GiB at concurrency 16. The client
streams deterministic PDF-shaped bytes without a chunk-sized retained buffer.

All PATCH responses must be 204 with the correct offset. After sampling ends,
the server verifies every real session is active at its declared upload length
and hashes the stored bytes; final HEAD verifies offset and length again.
An invalid-session rejection or incorrect offset fails the run and emits no
successful measurement result. Completion routes are blocked. No `/complete`,
database, finalizer or background queue runs in this PATCH profile. The protocol
derives from the existing standard-handler E2E, while isolating its PATCH phase.

## PATCH result schema and interpretation

Successful verbose test output contains one `GOUPLOADS_PATCH_MEASUREMENT`
JSON object, schema `gouploads.patch-measurement.v1`, status `MEASURED`.

- Identity/config: `profile=local_file_store`, `adapter=standard_net_http`,
  `buffers=fresh_process_no_warmup`, `config.{chunk_mib,concurrency,
  admission_budget_mib}`, `bytes_per_upload`, `body_limit_bytes=33554432`,
  `sample_period_ns=5000000`, `connection_reuse`, `cgroup_v2_memory_max`.
- `server`: `baseline_after_gc`, `workload_end_before_gc`, `retained_after_gc`.
  Each has `rss_bytes`, `heap_alloc_bytes`, `live_heap_at_last_gc_bytes`,
  `total_alloc_bytes`, `num_gc`, `gc_pause_total_ns`.
- Server peaks: `sampled_peak_rss_bytes`, `sampled_peak_heap_alloc_bytes`,
  `sampled_peak_live_heap_at_last_gc_bytes`, `sample_count`.
- Server environment: `go_version`, `gomaxprocs`, `gogc_env`, `gomemlimit_env`.
- Correctness: `verified_sessions`, `verified_bytes`, `payload_sha256`,
  `correctness`, `complete_and_background_jobs_excluded=true`.
- Deltas: `allocated_bytes_per_patch_including_observer`, `workload_gc_cycles`,
  `workload_gc_pause_ns`; baseline/end counters permit independent recalculation.
- Client timing: `client_wall_ns`, `throughput_bytes_per_second`,
  `client_patch_latency.{all,first_full,steady_full,final_short}` containing
  `count,p50_ns,p95_ns,p99_ns,max_ns`; nearest-rank quantiles.
- Bounded raw `client_patch_samples` entries: `kind,bytes,nanoseconds`.
  At most 208 PATCH samples per supported point; no session IDs or payloads.

Peaks are sampled lower bounds, not exact maxima. `HeapAlloc` includes objects
not yet collected; `live_heap_at_last_gc_bytes` is the runtime's last-GC live
estimate, not an instantaneous live-object census. Retention is after one
explicit GC without forcing page scavenging; retained heap is not retained RSS.
The 5 ms observer, its allocations and pipe coordination are included in the
server window. All byte hashing/verification and final HEAD are excluded.
Client latency/throughput includes payload generation, network and scheduling;
do not label it pure server service time. Server process memory excludes client
heap, but the processes still compete for CPU and container memory.

This first profile has local fsync and the FileStore's 128 hashed lock slots;
independent sessions can collide. It has no S3 multipart/lease, direct-Fiber,
same-session conflict or warmed-process comparison. Those need separately
labelled future profiles; do not extrapolate these results to them. Run race
checks separately from performance measurements.

## Original finalizer: exact manual invocation

The finalizer fixture reuses the existing owned-database setup and integration
helpers. It requires an already available PostgreSQL service and an owned
disposable database using the existing `TEST_PSQL_*` configuration. The wrapper
discovers/configures an existing service; it does not provision one. The fixture
forces local storage even if S3-related environment variables are present.

```sh
# Wrapper contract tests only; do not connect to PostgreSQL.
"$GO" test -tags integration ./integration/originals \
  -run '^TestFinalizerLockProbe' -count=1

# Both normal_gated and cancel_gated; one source size per invocation.
GOUPLOADS_MEASURE_FINALIZER_LOCK=1 GOUPLOADS_MEASURE_FINALIZER_MIB=1 \
TEST_PSQL_MAX_CONN_COUNT=3 \
sh ./scripts/with-integration-postgres.sh "$GO" test -tags integration \
  ./integration/originals -run '^TestIntegrationOriginalFinalizerLockMeasurement$' \
  -count=1 -v -timeout=3m
```

Supported source sizes are 1, 16 and 128 MiB. This first fixture is one worker
with an independent competing transaction, not a worker-throughput matrix.
At least three pool connections are required for finalizer, waiter and observer.
Migrations, queue setup, payload creation, initial staging, retries and cleanup
are outside the recorded finalizer timeline. No queue worker is started; the
fixture invokes the existing finalizer directly and checks durable job counts.

The wrapper observes BEGIN call/return, the repository's locking ScanOnex
call/return (including the scan), storage open/read/save, and real pgx
COMMIT/ROLLBACK call/return. It forwards the existing contexts unchanged.
The local storage gate deliberately pauses SavePersist while a separate
connection executes `SELECT ... FOR UPDATE` on the same synthetic fixture row.
A 5 ms observer uses only those two backend PIDs and `pg_blocking_pids` to verify
that this finalizer is the waiter's blocker. Once observed, the gate is held an
additional 25 ms, then released or cancelled. Waiter scan completion proves
eventual lock release independently of callback or transaction-client return.

Afterward, normal completion verifies stored bytes, checksum, metadata, one
after-job, one staging-cleanup job and one event. Cancellation checks
unchanged retry metadata, unchanged jobs/events and retained source bytes; a
retry must succeed. Repeated completion must not duplicate jobs or events.
Four helper tests cover scan completion/context, transaction completion/context,
storage context/read accounting, and gate cancellation before writes.

## Finalizer result schema and interpretation

Each successful scenario logs `GOUPLOADS_FINALIZER_LOCK` followed by JSON:

- `schema=1`, `scope=original_finalizer_sql_row_lock`, `scenario`,
  `storage=local`, `source_bytes`, `go_version`, `gomaxprocs`, `pool_max_conns`,
  `worker_concurrency=1`.
- `poll_interval_ns=5000000`, `injected_minimum_hold_ns=25000000`,
  `blocking_verified`, `correctness_verified`, `terminal_outcome`.
- `source_read_calls`, `source_read_bytes`, `source_read_call_total_ns`.
- `intervals_ns`: `begin_call`, `locking_query`, `client_transaction`,
  `post_acquisition_client`, `waiter_query`, `storage_open`,
  `storage_save_including_gate`, `storage_gate`.
- Ordered `events` with `phase`, `offset_ns`, optional `outcome`; outcomes are
  only `ok`, `canceled`, `deadline` or `error`. No raw SQL parameters, user/file
  identifiers, paths, signed URLs, payloads or credentials are exported.

The artificial gate, observer overhead and scheduling are included. This is a
controlled contention diagnostic, not an ungated production latency estimate.
Read-call time overlaps SavePersist and must not be added to it. BEGIN timing
includes pool acquisition and transaction startup, not isolated pool wait.
The post-acquisition interval ends at client-observed commit/rollback return;
it is not exact server lock-hold duration. The waiter may receive its response
before the original client's commit/rollback returns, so no ordering is assumed
between those two responses.

Cancellation retains the pinned transaction manager's original cancelled
rollback context. Rollback-return errors stay visible and do not themselves
prove lock release; the independent waiter supplies that evidence. Only the
waiter's own bounded cleanup uses a detached context. The fixture changes no
production cancellation or abort behavior. It does not measure S3 leases,
multipart abort, FinalizeUpload handoff/advisory locks, media callback locks,
isolated SQL/outbox time, queue latency, or finalizer throughput. Those remain
separate profiles; the known five-second multipart abort bound is not a bound
on this finalizer's SQL transaction.
