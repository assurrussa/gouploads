# Review hardening verification — 2026-09-30

## Current result and provenance

The complete 47-file overlay was applied to the clean base commit
`4737b158982eb595fde315b26a1309b310f47c9a`, reviewed and repaired in a full
checkout on branch `tasks/review-hardening-2026-09-30`.
**The source candidate builds and passes the local source gate. This report
records verification before commit/PR publication, not a production release.**
No release tag or production migration was performed during this review.

Before application, the package application script was inspected and its dry run
verified every manifest size/SHA-256, the base commit and a clean tree. At the
start of this review, the remote candidate branch and master both pointed to the
base. The attached reports are historical evidence, not instructions to publish
or deploy the candidate. No partial detached Git tree was used as the source.

The original package author reported stdlib-only checks under Go 1.23.2 without
PostgreSQL/MinIO or a full build. Those limitations describe that earlier run;
the full-checkout results below supersede them for the locally repaired source.
The original application-script test report was supplied with the package and
was not independently rerun here.

## Repairs established by review

- Reject colliding canonical preset names before downloads or storage writes.
  The regression failed for case, repeated-separator and whitespace collisions
  before the fix, and passes with no artifact/job side effects after the fix.
- Apply effective ingestion defaults and the absolute upload cap to TUS creation
  and resume. Zero limits no longer bypass the default temporary-storage limit;
  invalid negative configuration fails before allocation.
- Accept valid small local PATCH requests by inspecting a bounded, locked private
  prefix across chunks. Incomplete signatures are not approval; an invalid
  continuation is rejected before Append. Real Fiber/local-store PNG tests cover
  one-byte and four-byte PATCH requests. A separate regression reproduced a
  custom-store prefix longer than 512 bytes causing a negative slice bound; the
  prefix is now bounded before appending request bytes, and the HTTP race tests pass.
- Repair test mock imports, configured lint/format failures and the legacy DI
  constructor call while preserving its explicit media mode. Update completion
  E2Es for ready-session retention and verify the same FileID on replay.
- Exercise policy, scanner, batch errors and finalization keys through supported
  public packages; update host integration docs and the changelog. Generated
  options/mocks were regenerated through the configured tools, not edited by hand.

Independent reviews inspected immutable source snapshots. The final review found
no blocking code defects in handoff/deletion, storage confinement, MIME/limits,
HTTP policy, canonical artifacts or scanner spooling. It identified the missing
live handoff/cleanup contention proof. The added live regression now passes
three times with race in both commit orders, using PostgreSQL advisory-lock
waiters and actual file/outbox rows. The later bounded-prefix fix was separately
reviewed without additional findings.

## Checks actually executed

Environment: Go **1.27.1**, macOS/arm64; configured golangci-lint **2.13.1** and
additional exact workflow-version **2.14.0**. Dedicated disposable PostgreSQL 17
and MinIO `RELEASE.2025-09-07T16-13-09Z` fixtures were used, without production
storage or existing application containers. Both review fixtures were removed
after the final tests. The module/toolchain declarations and dependency files
were preserved.

Passed:

- `go build ./...`.
- `make source-readiness`: tidy diff, gofumpt/gci, vet, configured lint, default
  race/coverage tests, public surface including integration-tag hosttest, real
  PostgreSQL TUS, private S3 URLs, local and S3 media E2Es, original-only E2Es,
  clean local external consumer, anonymous dependency downloads and sibling-host
  import policy. Local execution used a prepopulated module-cache override.
- `go test -race -tags=integration ./... -count=1` against the isolated fixtures.
  This includes real repository handoff/rollback/primary/deletion tests and actual
  migration up/down through the test database helpers.
- `go test -race -tags=integration ./domain/files/repositories/filerepo
  -run TestIntegrationHardeningMigration -count=1`: migration rejects duplicate
  active primary files and rollback refuses pending deletions or cleaning
  sessions, with transactional schema restoration checked after refusal.
- `golangci-lint run -v --timeout=5m ./...` with the workflow's **2.14.0** binary:
  **0 issues**. Repository lint settings were not relaxed.
- `make generate`, followed by repository `make fmt`: no generated-file diff
  remains after the configured formatting step.
- `GOOS=windows GOARCH=amd64 go test -c ./internal/filelock`: compilation only;
  the executable was not run on Windows.
- `actionlint .github/workflows/go.yml` with actionlint **1.7.12**. The workflow
  was validated locally; no hosted GitHub Actions run was triggered.
- Source-mode `govulncheck -show verbose ./...` (**v1.8.0**): **0 reachable vulnerabilities**
  and **0 affected imported packages**. Three module-only advisories concern
  unused SSH/OpenPGP packages in `golang.org/x/crypto v0.55.0`:
  [GO-2026-5932](https://pkg.go.dev/vuln/GO-2026-5932),
  [GO-2026-6354](https://pkg.go.dev/vuln/GO-2026-6354), and
  [GO-2026-6355](https://pkg.go.dev/vuln/GO-2026-6355).
  This is a reachability result, not a claim that every required module has no
  advisories. Dependencies were not upgraded solely for unused packages.

Additional focused checks passed:

- `go test -race -tags=integration ./domain/files/service/tusupload
  -run '^TestIntegrationHandoffCleanupSerializesCommitOrder$' -count=3`: both
  handoff-first and cleanup-first outcomes, actual file/outbox rows and a
  PostgreSQL lock-wait barrier. This test checks source ownership (`KeepObject`),
  while the separate MinIO E2Es exercise storage IO.
- `go test -race -tags=integration ./domain/files/repositories/filerepo -count=1`
  after the added migration guards and test-helper lint correction.
- `golangci-lint run --build-tags=integration` on both affected repository and
  TUS packages, version 2.14.0: **0 issues**.
- `go test -race ./domain/files/transport/http -count=1` after the custom-store
  prefix fix: passed, including the new panic regression and real local chunks.

The final HTTP prefix safeguard followed the passing source gate. Its unchanged
public-surface, clean-consumer, dependency-download and import-policy results are
reused. Final module `make check` with lint 2.14.0 and
`go test -race -tags=integration ./... -count=1` were rerun after that safeguard
and the two additional integration-test files: **both passed** on the final code.

The gopls MCP diagnostics retained stale pre-overlay file context and reported
inconsistent old test locations. Compiler, tests and configured lint results
were used as the authoritative checks. Its vulnerability summaries also lacked
reachability detail; the separate source-mode govulncheck resolved that ambiguity.
No claim of a clean current gopls diagnostics session is made.

## Deliberate limits

The clean consumer uses the local checkout. Anonymous source mode downloads the
public dependency graph; it does not build an anonymously published root module.
Published-version gates require the actual reviewed candidate tag and were not
run against an older tag. `make release-readiness` therefore was not substituted
for the untagged `make source-readiness` contract in `RELEASING.md`.

Sibling-host import compliance is not a full regression run of those applications.
Hosted Linux CI, real-browser TUS, Windows runtime locking, worker-process restart,
Ceph/provider-specific behavior and production data migration/restore were not
executed. Full module integration ran with race; a repeated whole-module five-run
race stress test was not stacked on the successful gate.

The S3 fixed-chunk profile, Fiber body buffering, long transactions across artifact
IO, best-effort live events and reconciliation of permanently failed uploads
remain explicit limits. Database lease fencing does not make external storage IO
transactional. The scanner is an optional integration port, not an antivirus
engine. Hosts still supply object authorization, moderation, bucket policy and
operations. Schema changes require the coordinated worker transition documented
in `review-hardening.md`.
