# Diagnostic candidate readiness matrix

The diagnostic API has focused implementation evidence; **aggregate merge
readiness remains pending**. `AGENTS.md` requests release-readiness for public API
changes. For this untagged candidate, `RELEASING.md` specifies:

> For an untagged candidate, run `make source-readiness` with the integration
> services available.

The authorized endpoint is an untagged draft PR. Do not create a tag, run a
published-version probe against another tag, or claim published reusability.
`source-readiness` includes `check`: tidy, format, whole-module vet/lint and one
full race/coverage pass. Run that aggregate with one heavy command at a time.
Its `check` constituent is the final required `make check` evidence for the
unchanged tree; do not stack another duplicate `make check` afterward. If a
repair changes source, rerun the affected checks and final gate as required.

Implementation/test source is `228293264f49c988dd3132de39db0d0a1e9cacfa`.
Subsequent evidence-only commits change Markdown. Earlier passing constituents
remain valid source evidence, but they do not substitute for an aggregate pass.
The aggregate will preserve and run every dependency below.

| Required leaf target | Existing evidence | Remaining scope/dependency |
| --- | --- | --- |
| `tidy-check` | Not run | `go mod tidy -diff`; must leave manifests unchanged |
| `fmt-check` | Touched Go files pass gofumpt/gci | Check every repository Go file |
| `vet` | Changed/public packages and integration-tagged repository pass | Whole module, normal tags |
| `lint` | Same scoped packages pass, zero issues | Whole module, normal tags |
| `test-full` | Focused non-race tests pass | Whole module `-race -cover -covermode=atomic -count=1` |
| `test-surface` | Pass at `2282932` | Aggregate repeats supported host/hosttest/consumer/boundary/probe checks |
| `test-diagnostics-postgres-integration` | PG18.6 pass: 19 subcases + 3 groups, 0 skips | Aggregate repeats with owned PG fixture |
| `test-surface-integration` | Not run as target | Integration-tagged hosttest surface; inspect outcomes |
| `test-tus-postgres-integration` | Not run on candidate | PostgreSQL session lease/CAS/fencing/finalize contracts |
| `test-media-continuation-postgres-integration` | Not run on candidate | Real PG outbox + HTTP stub, race enabled, persisted 30-second continuation |
| `test-source-url-s3-integration` | Not run on candidate | Private source object and direct presigned GET against pinned MinIO |
| `test-portable-s3-media-e2e` | Not run on candidate | PG + pinned MinIO + in-process artifact/media stubs, one/two-bucket cases |
| `test-portable-local-media-e2e` | Not run on candidate | PG + task-owned temporary local storage + in-process HTTP stubs |
| `test-originals-integration` | Not run on candidate | PG + MinIO + temporary local storage; real outbox, originals/audio/standard HTTP adapter; race enabled |
| `externalconsumer-local` | Both clean modes pass at `2282932` | Aggregate repeats normal/integration-tag local consumer probes |
| `anonymous-source` | Not run on candidate | Empty credential/cache environment; public proxy and checksum-network access |
| `import-policy-site` | Import-policy unit package passes | Strict actual consumer roots; all three roots verified present locally |

## Queued fixture and execution scope

- Wait for Outbox's explicit heavy-lane release. No aggregate, race command or
  new fixture starts while its window is active.
- Create one uniquely owned, loopback-only PostgreSQL fixture with bounded CPU/
  memory and ephemeral data. Override `TEST_PSQL_ADDRESS_LOCAL`/port explicitly;
  UUID databases/schemas belong only to this fixture. No production connection.
- Start one owned MinIO process on a loopback port and a new task-owned data
  directory. Use the source pin/checksums in `integration/test-services/minio-source.json`:
  `RELEASE.2025-10-15T17-29-55Z`, source commit
  `9e49d5e7a648f00e26f2246f4dc28e6b07f8c84a`.
  A previously built native binary is available; its recorded and freshly read
  SHA-256 match `a0b063e47bfcda1f7d91d0207eda72ebdca3c8d2a042ad988dbd82b7d6bb7574`.
  Reuse only the executable, never its old data or live process. No rebuild is
  currently needed. Fixture credentials are synthetic defaults required by the
  existing portable-media tests.
- Set `TEST_S3_ENDPOINT` explicitly, so originals/audio S3 cases cannot skip.
  Other media endpoints/artifacts are in-process HTTP fixtures; the gate does
  not require a production or live codec/media-resizer runtime.
- Set `IMPORT_POLICY_REPO_ROOT` to the verified local consumer workspace with
  `site/backend`, `goadmin`, `site/fixtures/second-go-host` present. The isolated
  clone's default parent exists but has no such siblings, so leaving the default
  would fail. This is a read-only host-source import scan, not a host build.
- Invoke unchanged `make source-readiness` serially with shared Go caches and
  explicit fixture environment. Preserve all prerequisites; record each leaf's
  outcome, inspect required integration cases for skips and record candidate
  revision/tree and service identity. On failure, report the failed leaf and
  distinguish new regression from existing/environment failure before repair.
- Clean up only owned database/container, MinIO PID/data, buckets and anonymous
  probe temporary roots. Do not stop another process or run broad cleanup.
  Release the lane when checks and owned cleanup finish.

Reserve approximately **20–40 minutes** for this window with warm shared caches
and the verified existing MinIO binary. This is a planning estimate, not a timed
promise: anonymous downloads and full race compilation may dominate, and the
continuation regression deliberately waits at least 30 seconds (75-second test
budget). Report actual durations and blockers when the gate runs.
