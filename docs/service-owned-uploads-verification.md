# Service-owned reader upload candidate verification

Candidate based on public master `cdc352c251e4a5a59f25e078d3115f692ca4247d`
(tree `0669bba1bf4d69dfc03d243426810c461612f87e`). This is source-candidate
verification, **not a published release or deployment claim**.

The tested source is PR #26 commit
`25917c3b8d0090c27cf6306838f9b133e79546f5`, tree
`2bace51da682d27f7bc8411b89781db484817459`. The follow-up receipt update
changes only this document; runtime, tests, module files and gate scripts remain
byte-for-byte identical to that tested tree.

## Passed on the patched Go 1.27.3 graph

- `make check`: tidy diff, gofumpt/gci, vet, configured lint, and the full
  race/coverage test suite.
- Full `govulncheck ./...`: zero reachable vulnerabilities and zero affected
  imported packages. The remaining module-only OpenPGP advisory is not imported;
  the crypto module is required through Fiber's ACME/autocert path.
- Public `host`/`hosttest` surface checks and a clean local-replacement consumer
  in ordinary and integration-tag modes.
- Real disposable PostgreSQL service metadata: NULL human columns, retained
  site/client JSON provenance after transient uploader clearing, finalization
  replay, and binding conflict. Existing concurrent atomic finalization,
  lifecycle diagnostics, TUS fencing and durable continuation restart gates pass.
- Loopback local media E2E with the real PostgreSQL file repository: service
  reader admission, configured resizer dispatch, callback listener, artifact
  finalization, original/main/thumbnail bytes and checksums, retained provenance,
  explicit after-job exactly once across finalizer/callback replays, unchanged
  user notification count, and cross-site finalization-key rejection. The
  portable-media harness uses its existing outbox collector; real queue restart
  persistence is covered separately by the continuation gate.
- Human admin/user regression tests and byte-for-byte legacy finalization binding
  compatibility pass. Invalid/mixed service identities, replacement deletion,
  user-targeted/default after-jobs and invalid UTF-8 client IDs are rejected.

## Cross-environment acceptance completed on 2026-10-26

The initial dot sandbox could build the pinned MinIO source but could not start
it because interface enumeration was denied. This is a limitation of that
execution environment, not a passing S3 result. A separately authorized cloud
environment subsequently tested the exact commit/tree above with live MinIO and
PostgreSQL. The MinIO fixture remains pinned by
`integration/test-services/minio-source.json`; no provider substitution or
network-policy bypass was made.

The cloud run passed live S3 source-URL acceptance, portable S3 media processing,
and race-enabled original-only acceptance on both local and S3 storage.
`make prepare` produced no diff. `make publish-readiness VERSION=v0.14.0`
passed its prerequisites up to `anonymous-source`, where the isolated process
could not resolve `proxy.golang.org`. That aggregate invocation therefore exited
nonzero; it is not represented here as a single successful full invocation.

A separately authorized macOS run on the same exact commit/tree then passed the
original, unmodified `make anonymous-source`: empty inherited environment,
fresh caches, authentication disabled and no inherited proxy. No cached or
local-replacement download was substituted. The source gate proves public
availability of the dependency graph, not publication of the root module.
The following `import-policy-site` target also passed separately on macOS using
`make import-policy-site IMPORT_POLICY_REPO_ROOT=<clean-consumer-root>`.
All three configured consumer roots were present, populated with complete tracked
Go source from clean real consumer checkouts: site commit
`4de906fc3748e00eccc24b31c5694df64b170749` (560 Go files) and goadmin commit
`081484d8937c540b747224f873845b810d76a0ed` (513 Go files). This verifies the
tracked consumer sources, not uncommitted or ignored generated artifacts;
no absent-directory skip was used and the original checkouts remained clean.

Together these results close the source pre-merge checks. The cloud prepare
result had no generated/source diff; this follow-up changes only the receipt,
leaving all tested runtime inputs byte-for-byte unchanged.

## Remaining publication gates and limitations

An immutable published tag and its clean published-version and anonymous
published-consumer gates remain required. The planned version `v0.14.0` was
available when checked, but this is not a tag creation or release claim.
No merge, tag, release or deployment is established by this source receipt.

The additional integration-tag lint diagnostic reports pre-existing helper
findings outside the new service code; the repository's required ordinary lint
passes. Do not confuse that optional diagnostic with a clean integration-wide
lint result.

No new CI triggers were added. The verified base contains no checked-in GitHub
Actions workflows; empty status/run lists are not a substitute for these gates.

## Compatibility note

The upload API/storage additions are opt-in and preserve human upload behavior.
The security release intentionally raises the minimum compiler to Go 1.27.3 and
updates x/net to v0.60.0, with its required x/crypto v0.57.0 and x/text v0.42.0
closure. This compiler-floor change is separate from API backward compatibility.
Hosts remain responsible for authentication, site/object authorization,
immutable generations/originals and idempotent callback CAS updates.
