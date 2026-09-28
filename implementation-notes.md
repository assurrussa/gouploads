# Implementation Notes

## 2026-07-21: Complete TUS and replacement cleanup lifecycle

- Published commit `1d423eacf3c9aa7017dfa8f66fd353eb4110f294` as
  `v0.10.0-alpha.6`; the pushed tag passed the clean published-consumer gate.
- HTTP TUS completion now derives physical filename/folder/path only from the
  normalized `CompleteResult.RelativePath`; the client filename remains only
  in `OriginalName`.
- Kept exact staging cleanup (`FileID=0`) and old-record cleanup (`DeletedID`)
  as independent idempotent jobs. Replacement ownership is checked by
  `(ObjectType, ObjectID)` both before task creation and before deletion.
- Old-record deletion now sends the deduplicated current preset paths,
  `GetFullPath`, and legacy preset paths to `DeleteBatch`. The replacement job
  is not enqueued until all new artifacts and their transaction are durable.
- Corrected unfinished-session expiry so a stored `expires_at` is compared with
  the current expiry threshold once instead of applying `SessionTTL` twice.
- Added structured cleanup logs and a real Fiber HTTP/TUS production-shaped
  E2E covering mismatched client/physical names, replacement, no replacement,
  foreign ownership, partial preset failure, idempotency and TTL timing.
- Site adoption and Dokploy deployment completed at `site v0.0.24`. The live DB
  audit found no unfinished sessions, queued jobs, failed jobs or confirmed S3
  orphans, so no manual object deletion was performed.
- The live storage contract probe exposed a separate deployment issue: the
  empty staging-bucket override falls back to the public bucket, so unsigned
  staging GET returns 200 instead of the required 403. This does not change the
  alpha.6 deletion logic and needs a separate bucket-policy/topology decision.

## 2026-07-21: Independent object and replacement file identifiers

- Removed validation that rejected uploads when `object_id` numerically matched
  `replace_file_id`. The values identify a host entity and a stored file,
  respectively, so equality is valid and does not imply self-replacement.
- Kept uploader, required input, and object-type validation unchanged.
- Added focused regression coverage for both multipart `SingleRequest` and TUS
  `ReaderRequest` validation with equal numeric identifiers.

## 2026-07-20: Portable S3 media implementation

- Started implementation from docs/tasks/portable-s3-media.md.
- The stable boundary remains host and hosttest; S3 adapter details stay
  internal.
- Chosen implementation order is public config/key/storage contracts first,
  then streaming finalization/checker, then host adoption and release evidence.
- Publication of v0.10.0-alpha.4 is tracked separately from local code
  readiness and will not be claimed before the pushed-tag clean-consumer gate.
- Replaced the former provider/proxy-oriented S3 config with a generic endpoint,
  delivery base, public prefix and optional staging bucket. The 2026-07-19
  source-host rewrite and the older ACL/quarantine notes below are superseded
  for the new portable contract; they remain historical context only.
- Renamed the deep generic adapter from `ceph` to `s3store`, stopped sending
  object ACLs, routed staging/public keys to the configured buckets, connected
  SDK retries and applied private/immutable cache headers.
- Finalization now validates same-origin signed artifact URLs, sends no API
  secrets, redacts signed queries, streams with bounded memory, calculates
  SHA-256 and commits DB metadata/outbox cleanup only after every final object
  is durable.
- Added the stable storage checker and a mandatory integration flow covering
  MinIO, PostgreSQL, a signed-artifact server, both bucket topologies,
  image/video/PDF, retry/partial cleanup, public Range, replacement and explicit
  deletion. `make test-portable-s3-media-e2e` passes.
- Context7 CLI documentation lookup was attempted three times but the local
  Node runtimes were unusable. The exact checked-in AWS SDK v2 source was used
  to verify retry, multipart, checksum and no-ACL call shapes before tests.
- Module unit tests and the focused integration gate pass. Publication,
  tagging/pushing and the post-tag clean-consumer gate were not performed.
- Final review made public and staging prefixes explicitly non-overlapping,
  restricted source presigning to the configured staging prefix, and made TUS
  expiry cleanup remove a completed staging object as well as its durable
  session. Normal post-handoff TUS session deletion deliberately retains the
  source until finalization enqueues object cleanup. Regression tests cover
  both lifecycle paths and the prefix invariants.
- Finalization cleanup now runs only on the terminal outbox attempt using
  outbox v0.10 job metadata, so a transient retry cannot delete deterministic
  keys already written by a successful concurrent/retried attempt. Duplicate
  completed callbacks are no-ops, a main artifact is mandatory, cleanup uses a
  detached bounded context, and provider-reported per-key `DeleteObjects`
  failures remain retryable.

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

## 2026-07-20: Portable S3 media architecture

- Replaced provider URLs in managed records with relative object keys and
  canonical edge composition through `StoragePublicConfig`.
- Renamed the internal generic adapter directory from `ceph` to `s3store` and
  removed ACL fields from multipart create/copy paths. Bucket policy now owns
  public access.
- Centralized explicit S3 validation/defaults so storage, TUS, source signing,
  DI, and the contract checker agree on endpoint, buckets, prefixes, TTL,
  timeout, credentials, and retry behavior.
- Kept the public `host.Storage` interface intact. Public/staging bucket routing
  is selected internally from the key prefix.
- Chose deterministic final keys based on the existing file-record slug and
  preset name. Extensions come from the validated artifact MIME, never the
  original filename or signed artifact URL.
- Kept media-resizer's signed-artifact webhook contract. Artifact downloads use
  a secret-free client, block redirects by default, restrict origin and MIME,
  redact signed queries, and stream with bounded size plus SHA-256 accounting.
- Enqueued staging deletion in the same DB transaction as final metadata and
  after-jobs. Partial finals and failed DB finalization schedule idempotent
  path-only cleanup jobs; those internal cleanup jobs no longer query file ID
  zero or publish staging locations.
- Added `host.StorageContractChecker` as an explicit operator probe. It avoids
  `ListBucket`, validates private staging and public HEAD/GET/Range behavior,
  and always attempts cleanup.
- Context7 documentation lookup was attempted as required, but the available
  CLI runtimes were broken locally. AWS SDK retry/presign behavior was instead
  verified against the exact installed module source before implementation.
- Final local verification passed on 2026-07-20: `make check` completed tidy,
  generation, formatting, vet, lint with zero issues, unit tests, repeated race
  tests and coverage. The semver check accepts `v0.10.0-alpha.4`.
- All pre-tag publish-readiness components passed against local dependencies:
  stable surface tests, `hosttest`, PostgreSQL TUS integration, direct
  presigned MinIO source integration, portable media E2E, local clean-consumer
  probe and the `site`/`goadmin` import policy. The final clean-diff assertion
  intentionally remains a post-commit release step.
- The portable media E2E was also re-run through the owning `site` facade,
  `task storage:test:e2e`, and passed without a skip.
- Hardened the lowest multipart helper to clear any ACL supplied by an internal
  caller before `CreateMultipartUpload`; tests now prove this invariant in
  addition to adapter-level create/copy assertions.
- Final verification re-ran every pre-tag component independently on
  2026-07-20: PostgreSQL TUS CAS/lifecycle integration, direct presigned MinIO
  source GET, stable surface and `hosttest` integration, local external
  consumer, cross-repository import policy, exact `v0.10.0-alpha.4` semver
  validation and the portable-media E2E all passed. The E2E was run both from
  this repository and through `site`'s `task storage:test:e2e` facade.
- A parallel external-consumer run initially hit a sandbox permission error in
  the shared Go build cache; the isolated rerun with a dedicated temporary
  `GOCACHE` passed both generated consumer probes. This was an execution-cache
  issue, not a module-contract failure.

## 2026-07-20: Host-side PostgreSQL release-gate discovery

- `make publish-readiness` previously passed the Docker-network default
  `integration-postgres-tests:5432` to Go tests even when Make ran on the host,
  so the gate failed at DNS resolution despite a healthy Compose PostgreSQL
  service published on a host port.
- Added one PostgreSQL integration wrapper shared by the TUS fencing and
  portable-media targets. Explicit `TEST_PSQL_ADDRESS_LOCAL` and
  `TEST_PSQL_PORT_LOCAL` values remain authoritative; otherwise the wrapper
  resolves the live Compose service by its stable service label and reads the
  actual published port instead of assuming a project-specific container name
  or fixed host port.
- Native non-Compose runners fall back to `127.0.0.1:5432`. Container runners
  retain the existing internal service DNS default. Dependency provisioning
  remains outside this reusable module's release gate.
- Multiple matching Compose services fail with an actionable override message
  rather than selecting an arbitrary database.
- Verification passed for shell syntax, explicit override preservation,
  automatic discovery of the live `127.0.0.1:54325` mapping, the focused TUS
  target, and every functional stage of
  `make publish-readiness VERSION=v0.10.0-alpha.4`. The aggregate target then
  stopped only at its intentional final `git diff --exit-code` because this
  implementation was still uncommitted.

## 2026-07-21: Complete media deletion graph

- Confirmed the production TUS leak came from mixing the logical client name
  with the physical S3 name. Remote completion now normalizes
  `CompleteResult.RelativePath` and derives `Path`, `FolderPath`, and
  `FileName` from that key; only `OriginalName` keeps the client value.
- Kept staging and replacement cleanup as independent `deleted_file` jobs.
  Staging uses the exact key and `FileID=0`. Replacement carries the expected
  `(ObjectType, ObjectID)` in its backward-compatible internal payload and is
  checked both before creating the new upload task and immediately before
  deleting the old record/files.
- Preserved numeric equality between `ObjectID` and `DeletedID`; they identify
  different entity kinds. A replacement is rejected only when the fetched file
  is absent or belongs to another object.
- Expanded old-media collection to include `GetFullPath`, every stored preset
  `RelativePath` (including `original`), and every non-main legacy preset path.
  Keys are deduplicated and sorted before the existing `DeleteBatch` groups
  them by staging/public bucket.
- Added structured cleanup logs with `file_id`, `deleted_id`, `staging_key`,
  and the full `public_keys` list. No signed URL or credential is logged.
- Fixed the double TTL without changing the public cleanup request: the S3
  store converts the host activity threshold to the `expires_at` threshold
  once; orphan multipart cleanup continues using the activity age.
- Reworked the portable MinIO/PostgreSQL test to traverse real Fiber TUS
  POST/PATCH/complete mapping. It proves differing client/physical names,
  independent staging/replacement jobs, ownership rejection, a failed new
  preset preserving the old media, full main/original/preset plus legacy
  cleanup, availability of all new variants, and repeated-job idempotency.
- Pre-tag verification passed on 2026-07-21: the full `make check` gate
  completed generation, formatting, vet, 72 linters, unit tests, repeated race
  tests, and coverage; stable-surface, `hosttest`, PostgreSQL TUS, direct S3
  source, portable-media HTTP/TUS E2E, local clean-consumer, and cross-repo
  import-policy checks also passed. The published-consumer gate remains a
  post-tag check for `v0.10.0-alpha.6`.

## 2026-07-22: Complete local media pipeline

- Added `StorageLocalConfig.SourceBaseURL` as a dedicated dispatch-time origin
  for a separate media-resizer. It composes only confined
  `tmp/uploads/...` references; public completed URLs still use the existing
  local delivery base/application domain. An empty source base preserves the
  previous shared-filesystem pass-through behavior.
- Changed local persistent writes to a same-directory temporary file plus
  atomic rename. Temporary uploads retain exclusive creation. This lets a
  retry replace deterministic partial final keys without exposing a partially
  rewritten file or destroying the prior complete attempt on reader failure.
- Fixed image-dimension inspection for reader/TUS uploads by replaying every
  byte consumed by `image.DecodeConfig` before storage. The source stream is no
  longer empty or truncated after metadata inspection.
- Added a PostgreSQL-backed local media E2E through real Fiber TUS
  POST/PATCH/complete mapping. It fetches the source through the configured
  HTTP origin, retries a failed preset, preserves the old media until all new
  variants are durable, removes current/original/preset/legacy files, verifies
  public GETs, rejects a foreign replacement, and repeats cleanup jobs.
- Verification passed for the full `make check` gate and every functional
  stage of `make publish-readiness VERSION=v0.10.0-alpha.7`, including both
  portable-media E2Es, PostgreSQL TUS fencing, the source URL integration,
  public-surface tests, a generated clean consumer, and the site import policy.
  The pre-commit gate stopped only at its intentional final dirty-tree check.

## 2026-07-31: Development gate efficiency

- Preserved all existing release, surface, integration, consumer, and import
  policy target names.
- Split mutating preparation from source-read-only `make check` and combined
  normal race plus coverage execution into one package traversal.
- Kept repeated race stress and HTML coverage available explicitly; the
  publish gate still prepares first and rejects generated or formatting drift.

## 2026-09-29: Standalone dependency and consistency boundary

- Follow-up to merged PR #6: keep asynchronous original-only uploads and explicit
  media mode; remove unused Redis contracts and private shared/WebSocket imports.
- UUID values preserve UUIDv4, JSON/text and SQL representations. The Go types
  are now library-owned; hosts adapt old identity and event transport types.
- `host.EventPublisher` only publishes. `NewOriginalRuntime` accepts nil events;
  configured DI receives a host-registered publisher and standard HTTP client.
- Deletion reads ownership and storage paths under the finalizer's row lock in
  the same transaction. A blocked deletion observes completed metadata.
- Validation passed: real PostgreSQL/MinIO original-only E2E (race, three runs),
  media regressions, `make source-readiness`, public consumer, import policy and anonymous
  dependency probe. Published anonymous root resolution remains blocked by
  repository visibility; no reachable vulnerabilities found by govulncheck.
  No tag, consumer migration or production deployment is part of this change.
