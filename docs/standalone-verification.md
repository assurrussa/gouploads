# Standalone implementation and verification notes

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
