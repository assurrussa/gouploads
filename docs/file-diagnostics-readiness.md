# Diagnostic candidate readiness matrix

Status: historical verification record for [PR #15](https://github.com/assurrussa/gouploads/pull/15),
merged on 2026-10-07 as `314ea8e9327b95ed05a5d26527d1fdbf7dee11f0`.
The fixture instructions and timings below describe that completed candidate
window, not pending work or a verification claim for the current master.
The diagnostic API remains merged but unreleased, outside `v0.12.0`.

The diagnostic API has focused implementation evidence and **the complete
untagged `make source-readiness` aggregate passed once** at
`b8b44efe34ca105a850536bf2decfd43a3c440e1` on 2026-10-07.
This includes all five `make check` constituents; no duplicate check was run.
Final reviewer approval was the next step at that point. `AGENTS.md` requests
release-readiness for public API changes. For this untagged candidate,
`RELEASING.md` specifies:

> For an untagged candidate, run `make source-readiness` with the integration
> services available.

The authorized endpoint for that window was an untagged draft PR. Do not create
a tag, run a published-version probe against another tag, or claim published reusability.
`source-readiness` includes `check`: tidy, format, whole-module vet/lint and one
full race/coverage pass. Run that aggregate with one heavy command at a time.
Its `check` constituent is the final required `make check` evidence for the
unchanged tree; do not stack another duplicate `make check` afterward. If a
repair changes source, rerun the affected checks and final gate as required.

Implementation/test source is `228293264f49c988dd3132de39db0d0a1e9cacfa`.
Subsequent evidence-only commits change Markdown. Earlier passing constituents
remain valid source evidence, but they do not substitute for an aggregate pass.
The aggregate preserved and ran every dependency below.

| Required leaf target | Final result |
| --- | --- |
| `tidy-check` | PASS; manifests unchanged |
| `fmt-check` | PASS; all repository Go files |
| `vet` | PASS; whole module |
| `lint` | PASS; whole module, 0 issues |
| `test-full` | PASS; whole module race + atomic coverage, count=1 |
| `test-surface` | PASS; all supported/boundary/probe packages |
| `test-diagnostics-postgres-integration` | PASS; PG18.6; earlier exact-source JSON evidence proves 19 subcases + 3 groups, 0 skips |
| `test-surface-integration` | PASS |
| `test-tus-postgres-integration` | PASS |
| `test-media-continuation-postgres-integration` | PASS; race enabled, 33.861s |
| `test-source-url-s3-integration` | PASS; actual pinned MinIO |
| `test-portable-s3-media-e2e` | PASS; actual pinned MinIO/PG |
| `test-portable-local-media-e2e` | PASS; owned local storage/PG |
| `test-originals-integration` | PASS; local/S3 originals, audio and HTTP adapter, race enabled |
| `externalconsumer-local` | PASS; both clean modes |
| `anonymous-source` | PASS; anonymous graph and downloads, no source build claim |
| `import-policy-site` | PASS; strict actual three host roots, explicit root override |

## Fixture and execution scope

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

## Completed run evidence

One unchanged aggregate invocation exited 0. No manifest/source mutation occurred,
no required gate was dropped and no duplicate `make check` was stacked. Explicit
S3 and PostgreSQL endpoints selected owned services. The aggregate log has no
skip marker; required originals/audio S3 skip conditions were disabled by the
explicit reachable endpoint. The prior JSON diagnostic run proves all diagnostic
subcases individually and remains exact implementation-source evidence.

The run used PostgreSQL 18.6 and the checksum-verified pinned native MinIO binary
listed above. Afterward, PostgreSQL showed zero diagnostic schemas and only its
bootstrap databases. Its owned container was removed. The owned MinIO PID was
verified against its executable/data directory, stopped, and only its new data
directory removed. Shared caches and prior service artifacts were preserved.
The heavy lane is released. No tag, published-version probe, production action
or recovery mutation occurred.
