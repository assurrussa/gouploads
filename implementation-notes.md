# Implementation Notes

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
