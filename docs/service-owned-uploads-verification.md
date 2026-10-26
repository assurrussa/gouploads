# Service-owned reader upload candidate verification

Candidate based on public master `cdc352c251e4a5a59f25e078d3115f692ca4247d`
(tree `0669bba1bf4d69dfc03d243426810c461612f87e`). This is source-candidate
verification, **not a published release or deployment claim**.

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

## Remaining gates and limitations

The source-built MinIO test service matches the existing recorded release,
commit and Go module checksums in `integration/test-services/minio-source.json`.
Its binary built successfully, but this execution sandbox denies the netlink
interface enumeration performed before MinIO's main function. The supported
escalated launcher also failed during sandbox setup. MinIO was not patched,
network policy was not changed, and no external service was substituted.
Consequently the live S3 source-URL, portable S3 media and local+S3 originals
acceptance gates remain blocked, not passed.

The additional integration-tag lint diagnostic reports pre-existing helper
findings outside the new service code; the repository's required ordinary lint
passes. Do not confuse that optional diagnostic with a clean integration-wide
lint result.

Anonymous-source download, preparation/generated-file reproducibility and final
release readiness must be recorded before release. An immutable published tag
and its anonymous/published consumer gates are still required. No new CI triggers
were added. The verified base contains no checked-in GitHub Actions workflows;
empty status/run lists are not a substitute for these gates.

## Compatibility note

The upload API/storage additions are opt-in and preserve human upload behavior.
The security release intentionally raises the minimum compiler to Go 1.27.3 and
updates x/net to v0.60.0, with its required x/crypto v0.57.0 and x/text v0.42.0
closure. This compiler-floor change is separate from API backward compatibility.
Hosts remain responsible for authentication, site/object authorization,
immutable generations/originals and idempotent callback CAS updates.
