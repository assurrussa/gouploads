# Implementation Notes

## 2026-07-17: Host-owned file object types

- Replaced the closed legacy `FileObjectType` allowlist with a bounded safe
  identifier contract so hosts can attach uploads to domain-owned objects such
  as `post` and `author` without importing or modifying upload internals.
- Kept path and metadata safety explicit: identifiers are ASCII lowercase
  snake-case tokens, start with a letter, and are limited to 64 bytes.
- Added focused acceptance and rejection tests. Existing built-in object type
  constants remain source-compatible and valid.

## 2026-07-18: Deterministic MockGen Refresh

- Accepted the repository's current source-mode `toolsmocks` output for 19
  mocks. Their generated APIs, imports, and method bodies are unchanged; the
  final formatted diff is limited to MockGen source/command headers produced
  by the checked-in generator/tool versions.
- This removes persistent clean-generation drift from `make check` and the
  consuming `site` repository's `task platform:repo-check` without changing the
  public `host` or `hosttest` surfaces.

## 2026-07-14: Stable v0.9.0 Promotion

- Promoted the verified durable multi-replica TUS and quarantine-promotion
  contracts from `v0.9.0-alpha.0` to stable `v0.9.0` without changing their
  runtime semantics or supported `host`/`hosttest` import boundaries.
- Kept PostgreSQL fencing, integration-tag consumer compilation, strict import
  policy, and the published clean-consumer probe as mandatory release gates.
- Corrected the workspace-root import-policy invocation so sibling `goadmin`
  and `site` consumers are scanned together without weakening missing-root
  failures.

## 2026-07-11: Quarantine Promotion Boundary

- Added `host.NewQuarantinePromoter` as the supported post-validation boundary
  that moves a completed object out of the private quarantine prefix and
  returns checksum, stable public URL, size, MIME type, and dimensions for CMS
  media metadata.
- Promotion is idempotent by the durable TUS finalization key. An ambiguous
  storage commit is recovered only when the deterministic destination object
  exists and its bytes still match the completed upload size.
- Promotion rejects incomplete metadata, unsafe finalization keys, source paths
  outside the configured quarantine prefix, destinations inside quarantine,
  and relative or non-HTTP public URLs. TUS completion alone still does not
  make an object public.
- Reused the existing storage contracts through aliases on `gouploads/host` so
  consumers do not need to import infrastructure packages to construct the
  promoter. This is an additive facade promotion; storage ownership remains in
  `gouploads` and CMS owns validation policy and media metadata.
- Made the expired-quarantine cleanup fixture derive its threshold from the
  created session timestamp. The previous fixed repository clock became stale
  relative to the production clock used by `S3Store.Create` and made the full
  repeated race gate date-dependent.

## 2026-07-10: Durable Multi-Replica TUS Foundation

- Replaced Redis-only S3 TUS session state in the production DI path with a
  PostgreSQL `upload_sessions` state machine. Redis is no longer required to
  resume an S3 upload after a restart or on another web replica.
- Added database-clock leases and monotonic revision fences. PATCH commits are
  conditional on session ID, expected offset, lease owner, unexpired lease,
  and exact revision; stale workers cannot advance durable state.
- S3 multipart uploads now start under a configurable quarantine prefix with
  private ACL and SHA-256 checksums. The configured general S3 ACL is
  deliberately ignored for incomplete TUS objects.
- Added crash reconciliation through `ListParts`: a retry can accept an exact
  part number/size/ETag/checksum match after S3 succeeded but the database
  commit did not. Mismatched parts are re-uploaded only by the current fence
  owner.
- Extended cleanup with `ListMultipartUploads` reconciliation so old S3
  multipart uploads that have no durable PostgreSQL session are aborted after
  the retention threshold.
- Added fenced, repeatable finalization and `HeadObject` recovery for a crash
  after S3 completion but before the database status commit. A stable unique
  finalization key is returned for downstream CMS/media idempotency.
- Kept completed protocol objects quarantined. TUS completion does not grant
  public access; validation and promotion remain a host/CMS media adapter
  responsibility.
- Added public `host.NewTusStore`, TUS request/session/result aliases, status
  constants, and stable sentinel errors. Existing hosts can continue to use
  local filesystem mode; S3 construction without PostgreSQL now fails early.
- Added tests for two-store resume, concurrent PATCH, crash reconciliation,
  finalize-crash recovery, competing/repeat finalize, quarantine and orphan
  cleanup, private ACL, and a real PostgreSQL CAS/fencing flow. CI and release
  readiness now include the PostgreSQL integration gate.
- Fixed the formatter target so `gci` uses the same repository-only Go file
  list as `gofumpt`; repository-local module caches are no longer traversed or
  mutated by `make fmt`.
- Updated the release import-policy target for the current repository topology:
  `goadmin` is a sibling repository, not a directory under `site`, and is
  checked through its own repository root.
- Used the repository's existing AWS SDK for Go v2 version and verified the
  current `UploadPart`, `ListParts`, checksum, and completed-part fields before
  implementing recovery.
- Tradeoff: S3-compatible providers must preserve the requested SHA-256 part
  checksum in `ListParts` to reuse a crash-orphaned part. If they do not, the
  current fence owner safely re-uploads that part instead of trusting size or
  ETag alone.
- Tradeoff: every non-final PATCH must equal the configured S3 part size. This
  keeps TUS offsets and S3 part numbering deterministic; larger intermediate
  chunks are rejected instead of creating multipart gaps.

## 2026-06-04: Agent Project Initialization

- Goal: initialize project-facing agent guidance for future work in
  `gouploads` without changing code behavior.
- Used `$project-context-router` to resolve shared context through
  `AGENT_CONTEXT_ROOT`, then compared the local repository docs
  and code with the shared `gouploads`, `media-resizer`, and `outbox` platform
  pages.
- Kept `AGENTS.md` focused on agent workflow, source order, public import
  boundaries, commands, generation rules, and verification expectations instead
  of duplicating `README.md` or `docs/host-integration.md`.
- Added `docs/project-map.md` as the concise project fact sheet for package map,
  runtime flow, ownership split, config keys, verification gates, and
  cross-project context.
- Preserved the existing reusable-boundary decision: runtime consumers use
  `github.com/assurrussa/gouploads/host`; external tests use
  `github.com/assurrussa/gouploads/hosttest`; deep packages remain internal
  unless promoted through `reference/externalconsumer`.
- Did not touch the pre-existing modified `.github/workflows/go.yml`.
- A direct `go list ./...` first failed because the default user-level Go build
  cache is outside the sandbox. Re-ran it with repository-local `GOCACHE` and
  `GOPATH`, matching the Makefile cache strategy, and it succeeded.

## 2026-05-28: Reusable Boundary

- Goal: make `gouploads` reusable through explicit host-facing contracts rather
  than copied `site/backend` internals.
- Initial direction: keep `github.com/assurrussa/gouploads/host` as the only
  runtime embedding surface and `github.com/assurrussa/gouploads/hosttest` as
  the only external test-support surface.
- Host applications remain responsible for config/env mapping, auth/session
  context, route mounting, migrations, outbox lifecycle, upload strategies, and
  media-resizer URL/token deployment wiring.
- Added an embedded migration contract on the `host` facade so new hosts do not
  have to discover the `files` table SQL from `site/backend`.
- Added an external-consumer workflow test that compiles the expected host
  integration path through `host`/`hosttest`.
- Added `docs/host-integration.md`, `RELEASING.md`, and `make
  release-readiness` as the reusable-boundary gate.
- Set repo-local default `GOCACHE`, `GOMODCACHE`, and `GOPATH` in the Makefile
  because default user Go caches can be unavailable in sandboxed dev
  environments.
- Kept Go caches under ignored dot directories rather than `tmp/` so `go vet ./...` and
  `go test ./...` do not accidentally scan downloaded module cache packages.
- Added `cmd/externalconsumerprobe` so local and published clean-consumer checks
  are executable gates instead of manual documentation snippets.
- Added `make externalconsumer-published VERSION=<tag>` and tightened
  `CONTEXT.md` so temporary deep host imports are not described as acceptable
  reusable-boundary state.
- Tightened `cmd/importpolicy` so runtime host code may import only
  `gouploads/host`; `_test.go`, `tests`, and `testsupport` packages may also
  import `gouploads/hosttest`; deep imports are rejected in both runtime and
  test code.
- Added `hosttest.NewListenResizeRequestMatcher` as public test support for
  resize callback tests that need numeric metadata tolerance after JSON decode.
- Extended `cmd/externalconsumerprobe` to compile-check the normal consumer
  surface and the `-tags integration` `hosttest` surface, including
  `hosttest.PrepareDB` and database test option helpers.
- Made the `hosttest` integration surface own its database helper contract
  directly instead of aliasing `domain/files/tests`; this keeps `hosttest`
  independent from internal test-package init behavior.
- Added an explicit `test-surface-integration` target to `release-readiness`
  and expanded the integration clean-consumer probe to compile-check
  `hosttest.CleanUp` and `DBHelper` helper methods.
- Verified current local boundary with `make release-readiness` in
  `gouploads`: `go vet`, surface tests, local clean-consumer probe,
  `go test ./...`, and `site` import policy all pass.
- Verified `site` aggregate boundary with `task platform:repo-check`: goauth,
  goadmin, gouploads, gofiber, second-host fixture, and platform import
  baseline pass.
- Excluded `.go-cache` and `.cache` from both formatting file discovery and
  import-policy walking so repo-local caches cannot create false boundary
  violations.
- Updated the site outbox/background docs so they point at the backend upload
  adapter and `gouploads/host` contract instead of presenting deep
  `gouploads/domain/files/outbox/*` packages as host-facing APIs.
- Removed the public import-policy `--allow-deep-dir` escape hatch; the release
  checker is now strict even for backend upload adapter directories.
- Kept `site/goadmin/tests` on its own local integration DB helper until a new
  `gouploads` tag is published and the site modules are updated. Current site
  still resolves `gouploads v0.7.23`, so it cannot compile against new
  unpublished `hosttest` integration helpers yet.
- Started the site test-only Docker dependencies and verified the affected
  integration packages against PostgreSQL: backend `filesseed`, goadmin
  `filerepo`, goadmin `tests`, and `gouploads/hosttest`.
- Re-ran the final local gates after those checks: `make release-readiness` in
  `gouploads` and `task platform:repo-check` in `site` both pass on the current
  worktree.
- Updated the default import-policy consumer roots to include `goadmin` and
  made the external-consumer probe render imports from the configured module
  path, not only the default module path.
- Added focused tests for the integration probe module path and CLI go test
  argument/env builders so the published clean-consumer gate is less dependent
  on untested string assembly.
- Documented the `hosttest` integration build-tag split and the expected
  `TEST_PSQL_*` variables so consumer projects can use database helpers without
  importing internal `domain/files/tests` packages.
- Added `go mod tidy -diff` to `make release-readiness` so a reusable release
  cannot pass with stale `go.mod` or `go.sum` state.
- Marked Makefile release/test targets as `.PHONY` so readiness gates cannot be
  skipped by accidental files with matching names.
- Re-verified the current worktree after the final Makefile changes:
  `git diff --check` passes in both `gouploads` and `site`, `make
  release-readiness` passes in `gouploads`, and `task platform:repo-check`
  passes in `site`.
- Confirmed the latest local `gouploads` tag is `v0.7.23` and origin has no
  `v0.7.24` or `v0.8.0` tag. The next reusable-boundary release should use a
  new semver tag; `v0.8.0` is the cleaner choice because the public host/test
  surface and release gates changed.
- Tightened the generated integration consumer probe to resolve `hosttest`
  through the same module-path mapping helper used by the normal probe. This
  keeps fork/module-path probes honest before the published-version gate.
- Tightened the import-policy checker so requested consumer roots must exist.
  This prevents false-green boundary checks when `--repo-root` or a consumer
  path is wrong. The CLI default now points at the sibling `../site` repository,
  matching the current host release gate.
- Clarified `RELEASING.md` and `docs/host-integration.md` around the exact
  release sequence, the recommended `v0.8.0` tag for the current public-surface
  expansion, required consumer roots, and the rule that `hosttest` aliases must
  still be imported through `hosttest` rather than internal storage packages.
- Published-version readiness remains intentionally unclaimed until a new
  `gouploads` semver tag is committed, pushed, and checked through `make
  externalconsumer-published VERSION=<tag>`.
- Added explicit validation for `cmd/externalconsumerprobe` subprocess
  arguments before invoking `go list` or `go test`, so the clean-consumer gate
  can keep executing the Go tool without `gosec` suppressions.
- Split the reusable release gate into pre-tag `publish-readiness` and post-tag
  `release-readiness`. The former now runs the complete local check, durable
  PostgreSQL TUS integration, consumer probes, import policy, and a clean-diff
  assertion; the latter additionally resolves the exact published version.
- Selected `v0.9.0-alpha.0` as the expected prerelease for the new durable TUS
  migration and host constructor contract. This supersedes the older planning
  note that mentioned `v0.8.0`; `v0.8.2` is already the published baseline.
- Made the Makefile-owned Go, module, GOPATH, and linter caches authoritative
  defaults (while still allowing command-line overrides). Environment-provided
  system cache paths previously made the coverage gate fail in sandboxed hosts.
- Removed parse-time `go list` expansion from the coverage recipe. Running
  `go test ./...` directly keeps package discovery inside the exported
  repository-local cache environment and avoids an untracked partial profile.

# Yandex Object Storage multipart finalization compatibility

- Yandex Object Storage accepts per-part SHA-256 input but its `ListParts`
  response exposes only part number, size, and ETag. Requiring a non-empty
  remote checksum caused valid TUS uploads to fail during finalization with
  `multipart part 1 does not match durable state`.
- Finalization now always verifies the durable size and ETag and verifies
  SHA-256 only when the provider returns it. Crash reconciliation remains
  conservative: a remote part without a checksum is re-uploaded when no
  durable part commit exists, because size alone cannot prove content identity.
- Added a provider-compatible unit test whose fake `ListParts` omits checksum
  fields while `UploadPart` still receives and persists SHA-256.

## 2026-07-19: Private media source URLs

- Goal: keep S3/TUS objects private while giving the storage-agnostic
  media-resizer a time-limited HTTP GET URL at dispatch time.
- Decision: preserve the public `host.Storage` interface and introduce a narrow
  source URL resolver through the stable `host` facade.
- Decision: keep the unsigned source reference in `send_resize_file` outbox
  payloads. S3 signing happens only when the dispatch use case is about to call
  media-resizer.
- Decision: add explicit `STORAGE_S3_SOURCE_HOST` and
  `STORAGE_S3_SOURCE_URL_TTL`; do not infer the source proxy from
  `STORAGE_S3_HOST`.
- Decision: default the source TTL to six hours and reject non-positive values
  or values above the AWS SigV4 seven-day maximum.
- Decision: rewrite only scheme and host after signing. Preserve `Path`,
  `RawPath`, and `RawQuery` byte-for-byte so an upstream S3 service can verify
  the signature.
- Compatibility: an empty S3 source host returns the presigned origin URL;
  local storage returns the existing source URL unchanged.
- Out of scope: queue cleanup, zero job ID behavior, and terminal failure/DLQ
  redesign.

## Verification

- Focused source resolver, dispatch use case, host facade, and external
  consumer tests passed.
- `go test -tags integration ./infrastructure/storage/files/sourceurl -run TestIntegrationPrivateS3SourceThroughProxy -count=1 -v` - passed against
  private MinIO: the unsigned origin request returned 403 and the signed
  request through the host-rewriting proxy returned 200.
- `make check` - passed, including tidy/generate/fmt/vet, lint with zero
  issues, all tests, repeated race tests, and coverage.
