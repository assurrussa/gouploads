# Standalone implementation and verification notes

## 2026-09-30 public-readiness candidate

Branch: `feat/public-readiness`.
Verified with repository Go 1.27.1:

- `StandardUploadHandler` constructor fail-fast: `NewStandardUploadHandler` returns `ErrIncompatibleBodyLimit` when the effective body limit is smaller than the underlying TUS store's chunk size (default 32 MiB body limit, explicit `StandardUploadHandlerConfig.BodyLimit` required for larger chunk sizes).
- Full `net/http` context propagation: all hooks (`ContextBuilder`, `UploadResourceAuthorizer`, `ResolveActor`, `UploadStrategy.CanUpload`, `GetConfig`, `GetAfterJobs`, `ReadPrefix`) receive standard `r.Context()`.
- Standalone quickstart S3 `Public.BaseURL` configured via `url.JoinPath`, and `urlComposer` wires directly through `host.ComposeFileURL`.
- Standalone quickstart reference policy sets `TrustRouteGuards: false` with explicit authorizer callback.
- Quickstart Web UI hardened: upload concurrency guard, file type detection (returns null for unsupported files with UI validation error before upload), local upload closure on `/complete`, async task polling (`GET /files/tasks/:id`) until outbox finalization completes (`status == "completed"`), and S3 `publicUrl` display.
- `TestIntegration_StandardUploadHandlerE2E` passes: full TUS chunking (5 MiB), HEAD resumption, final chunk, application completion, completion idempotency, and background outbox finalization into storage with SHA-256 validation.
- `make check`, `make test-surface`, and `make externalconsumer-local` pass with 0 lint issues and race detection enabled.

### Constructor review follow-up

The verified follow-up was committed as
`2577d8cd713a9741f93aeb696725758ad6abf56e`, based on
`6c4b7c7712017092b0023769df8e1aef3da0ac92`, on the same branch. The checks below
ran on the worktree before that commit; its implementation and test diff matches
the verified snapshot.

- Nil and zero-value upload handlers return `ErrNilUploadHandler`; more than one
  optional config returns `ErrInvalidStandardHandlerConfig`. Both errors occur
  before Fiber construction. Existing 0/1-config behavior and nonpositive
  body-limit defaults remain covered.
- Removed the unused unpublished `MaxAutoBodyLimit` alias. The external-consumer
  test now exercises actual constructor failures with `ErrorIs`.
- `go test ./host ./reference/externalconsumer -run TestStandardUploadHandler
  -count=1` passed, including the new regressions. Edited-file gopls diagnostics
  and final `git diff --check` passed.
- Complete `make source-readiness` passed with Go 1.27.1 on macOS/arm64 and
  configured golangci-lint 2.13.1 (0 issues). This includes tidy/format/vet/lint,
  the full race/coverage suite, both public-surface checks, PostgreSQL TUS fencing,
  private S3 source URLs, S3 and local media E2Es, original-only and standard HTTP
  adapter E2Es, clean local consumer (default and integration tags), anonymous
  dependency downloads and sibling-host import policy.
- The source gate used isolated disposable PostgreSQL 17.9 and MinIO
  `RELEASE.2025-09-07T16-13-09Z`, with explicit environment values:

  ```sh
  TEST_PSQL_ADDRESS_LOCAL=127.0.0.1 TEST_PSQL_PORT_LOCAL=49437 \
    TEST_S3_ENDPOINT=http://127.0.0.1:49438 make source-readiness
  ```

  These ports belonged to this run; both fixtures were removed after the
  integration steps. Existing application containers were not changed.
- Independent read-only review found no actionable defects in the final adapter,
  regression tests, external consumer, README and changelog snapshot.

No candidate tag was created. Published-version gates and production/browser
acceptance were not run; the passing anonymous source probe checks dependency
availability, not a published root-module consumer build.

## 2026-09-29 follow-up candidate

Base: merged PR #6 (`e05fe44`). Branch: `tasks/standalone-decoupling`.
This record supersedes the earlier environment limitations for this candidate;
the historical checks below still describe only the earlier source snapshot.

Verified with repository Go 1.27.1:

- `make source-readiness` passes as one complete gate; it includes `make check`
  (tidy, formatting, vet, lint and the full race/coverage suite) and the checks below.
- Public surface, integration-tag surface and `externalconsumer-local` pass.
- PostgreSQL TUS fencing, direct S3 source URLs and both portable-media E2Es pass.
- `make test-originals-integration` passes on isolated PostgreSQL 18.6 and MinIO.
  The package also passed `go test -race -tags=integration ./integration/originals -count=3`.
  It checks reader, multipart single/batch and in-process HTTP TUS ingestion;
  queued state, bytes/SHA256, durable jobs, fresh workers, duplicate delivery,
  actual row-lock waiters, replacement ownership and finalization/delete races.
- `import-policy-site` passes for the configured sibling consumers without
  changing their code or pinned versions.
- Anonymous source graph/download passes with empty caches and no credentials.
  Anonymous published `v0.10.0-alpha.7` fails through the public proxy because
  the root module is private. It is a visibility diagnostic, not candidate
  release evidence; no candidate tag was created.
- `govulncheck` reports zero reachable or imported-package vulnerabilities.
  Three module-level `x/crypto` advisories concern unused packages.
- Independent review found no actionable introduced data/locking/API defects.

The normal Go diagnostics MCP is attached to a different checkout and cannot
resolve this worktree correctly; compiler, vet, lint and tests provide the
worktree verification. Generated options/mocks were regenerated with options-gen v0.58.0 and mockgen
v0.6.0 from the module versions, not hand-edited.

Remaining boundaries: no browser/live-socket TUS smoke, consumer application
regression, production deployment or published candidate consumer gate. Storage
failure/uncertain commit injection remains unit-level coverage. A fresh worker
is recreated in-process, not by killing an OS process. Holding a row lock across
storage IO remains the explicit throughput trade-off. Existing physical deletion
before database commit cannot atomically roll back storage; this change fixes
stale paths under concurrent finalization, not cross-system atomic deletion.
The hosted CI billing restriction is external to these local checks.


## 2026-09-28 candidate

Base: `3e0df76ff465d3e190713987857e735c0fdcea65`.
Branch: `feat/standalone-originals-default`.

### Decisions

The supported standalone constructor and configured DI default to originals.
The old unsupported deep constructor retains media behavior so this change does
not silently reinterpret deep integrations or existing persisted jobs. Original
and media task names are distinct. New handlers must be deployed before producers.

The original worker reuses artifact IO validation, checksum, destination naming,
metadata and after-job/cleanup primitives from the existing media finalizer. It
is a storage-backed input adapter, not a second multipart/TUS implementation.
A row lock spans the streaming copy and metadata/outbox commit; this costs longer
transactions but prevents concurrent original-worker finalization without a new
lease schema. Ambiguous commit results never trigger deletion of final keys.
Live UI events remain best-effort, and permanent-failure orphan reconciliation
is not claimed solved by this PR.

No generated options/mocks or dependency versions were edited. The supported
package list remains host/hosttest. The existing detailed media documentation
and historical implementation notes remain background; the current default
and migration contract are in `standalone-uploads.md` and the README.
Repository visibility, releases, tags, master, goadmin and site were not changed.
Shared wiki access/update and a history-wide secret audit were not performed.

### Checks actually executed

The execution container has Go 1.23.2. On that compiler, the following **selected
stdlib-only source files** passed tests with the race detector (five repetitions)
and `go vet`:

```sh
# From config:
GO111MODULE=off GOTOOLCHAIN=local go test -race -count=5 processing_mode.go processing_mode_test.go
GO111MODULE=off GOTOOLCHAIN=local go vet processing_mode.go processing_mode_test.go
# From domain/files/outbox/finalize_original:
GO111MODULE=off GOTOOLCHAIN=local go test -race -count=5 payload.go payload_test.go
GO111MODULE=off GOTOOLCHAIN=local go vet payload.go payload_test.go
# From domain/files/usecases/command/upload_file:
GO111MODULE=off GOTOOLCHAIN=local go test -race -count=5 original_keys.go original_keys_test.go
GO111MODULE=off GOTOOLCHAIN=local go vet original_keys.go original_keys_test.go
```

All added/changed Go files passed gofmt parsing/format checks. One combined
verification shell invocation exceeded its execution timeout during vet; the
remaining vet/key/format checks were rerun successfully in a separate invocation.

The actual attempt `GOTOOLCHAIN=go1.27.1 go version` failed while resolving
proxy.golang.org (container DNS connection refused). The repository still declares
Go 1.27.0 and toolchain go1.27.1; neither was downgraded. Private repository sources
were inspected and branch updates performed through the authorized GitHub connector.

### Not established by those checks

No complete repository build, module tidy, generation, project lint, full unit
suite, PostgreSQL locking/rollback integration, S3/MinIO TUS end-to-end test,
real original worker smoke, anonymous consumer build, or site/goadmin regression
run passed in this environment. The added integration-shaped unit tests use
transaction/storage fakes and have not been executed on the project toolchain.
The selected-file checks are NOT evidence that the full library compiles.
Keep the PR draft until project-toolchain checks and real infrastructure gates
are successful. CI results, when available, belong to the PR run, not to the
selected-file test record above.

### Required next verification

1. Run make check and make test-surface on the declared toolchain; fix any real
   compilation, generation, lint or behavioral failure before merge.
2. Exercise the new runtime against PostgreSQL and local storage, then MinIO:
   original bytes/checksum, worker restart, concurrent duplicate jobs, failure
   before/after metadata commit, replacement and exact staging cleanup.
3. Run existing TUS, private source-URL and portable media integration gates to
   prove the opt-in media pipeline remains usable. Those tests alone do not
   cover the new original worker.
4. Verify affected consuming applications; use publish-readiness with a planned
   VERSION before tagging, and release-readiness with an actually published tag.
5. Remove private Go dependencies in a separate compatibility-focused change;
   runtime independence is not anonymous module publication.
