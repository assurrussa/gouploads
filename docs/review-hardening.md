# Upload lifecycle hardening candidate — 2026-09-30

Base commit: `4737b158982eb595fde315b26a1309b310f47c9a`.
Status: implementation candidate, not a verified release. See
`review-hardening-verification.md` before applying or merging it.

## Database and deployment

Apply `20260930120000_upload_lifecycle_safety.sql` through the existing migration
runner; its embedded copy is included in `host.MigrationFiles`. Historical
migrations are unchanged. The new migration adds application handoff tombstones,
durable deletion plans, the `cleaning` TUS state and an active-primary uniqueness
constraint. It also backfills known file types from MIME metadata.

The migration deliberately stops on duplicate active primary files. Inspect and
resolve the domain choice first; it does not silently choose a winner:

```sql
SELECT object_type, object_id, array_agg(id ORDER BY id) AS file_ids
FROM files
WHERE is_primary AND object_id IS NOT NULL AND deleted_at IS NULL
GROUP BY object_type, object_id HAVING count(*) > 1;
```

Back up the database and test the migration on representative data. The backfill
and index build can take locks and time on large tables. Plan a maintenance window
or a separately reviewed online rollout for large deployments.

Pause upload writes and workers for the transition, apply the migration, deploy
all new workers and handlers, then resume traffic. Do not run legacy deletion or
media-finalization workers concurrently with the new lifecycle implementation.
Do not roll back the schema with pending deletion plans or cleaning sessions.
Rollback also removes idempotency tombstones and must not be used while retries
from the new producer remain possible.

## Application handoff and deletion

`ReaderRequest.FinalizationKey` carries the canonical UUID returned by TUS.
The PostgreSQL repository serializes that key, creates the file and outbox job
in one transaction and records `key -> binding hash -> file ID`. Repeats return
the existing file; a changed actor/object/name/size/replacement binding is rejected.
A deleted result does not get resurrected. Ordinary uploads with an empty key
retain their non-idempotent contract.

Generic HTTP completion retains the ready protocol session until its TTL. This
lets a client repeat completion after losing an HTTP response. Session expiration
still ends the protocol retry window. Keep the minimal application tombstone at
least as long as any possible retry; this candidate does not automatically prune
it. Reader and stored handoff dependencies must share the transaction context.

S3 cleanup atomically claims `cleaning` with a lease and revision before external
IO, under the same handoff advisory lock. It rechecks expiry after selection.
Once a file/outbox handoff exists, the finalizer, not the TUS cleaner, owns the
staging object. A dead-letter finalizer therefore still requires safe operational
reconciliation; do not configure blind bucket TTL deletion of active sources.

File deletion first commits a frozen artifact plan and hides the file. Physical
purging happens outside the database transaction. Only after successful purging
are completion and after-jobs committed. A storage failure leaves a hidden,
retryable record, not an active database record pointing to deleted bytes.
`CleanupExpiredFiles` excludes records with pending deletion plans. The existing
outbox retry/dead-letter lifecycle must remain running and monitored.

Both original and media finalizers serialize on the file row. An uncertain commit
never triggers final-key deletion. `CleanupOnFailure` remains decodable for old
payloads but does not authorize destructive final-key cleanup. Long transactions
across artifact IO remain a deliberate trade-off: bound worker concurrency,
request deadlines and storage timeouts. This is not cross-system ACID.

Custom repositories must implement the new structural capabilities for durable
handoff, locking finalization and planned deletion. The shipped `host.FileRepo`
does; unsupported custom implementations fail closed instead of silently falling
back to unsafe behavior. Generated legacy interfaces/mocks were not hand-edited.

## HTTP policy and compatibility

Use `host.NewUploadHandlerWithPolicy` for an explicit security contract. Its zero
policy does not trust route guards or enable fallback strategies. Supply
`Authorize` to check access to the actual object for read/list/upload/resume/delete,
or explicitly set `TrustRouteGuards` only when all mounted routes already perform
those checks. Authentication alone is not object authorization.

The old constructor keeps the legacy empty-context default and route-guard-owned
authorization contract for compatibility. Unknown nonempty context names are
rejected by both constructors. Register every named context used by a client.
CMS routes still require an explicitly registered CMS strategy and do not inherit
generic complete/read/delete routes.

TUS identity-construction failures are rejected. A nonzero owner UUID is the
primary ownership identity; numeric ownership is a fallback only for old sessions
without UUID. Zero ownership is not a wildcard unless `AllowAnonymousTUS` is
explicitly enabled as a bearer-capability policy. Generic completion still needs
a valid upload actor. Legacy manager mapping is unchanged; `ResolveActor` permits
an explicit `(managerID, userID)` mapping for ordinary-user hosts.

A rejected first-chunk MIME check now returns a typed error to the top-level
handler: it cannot accidentally continue into Append after writing JSON.
TUS creation and resume apply the same effective limits and extension defaults
as ordinary ingestion, including the absolute upload cap. A zero strategy limit
does not grant unlimited temporary storage; negative configuration is rejected.
Local PATCH requests may accumulate a bounded private MIME signature across
small chunks. An incomplete signature is unapproved, and an invalid continuation
is rejected before writing it. Custom stores may implement offset-checked
`ReadPrefix` to support this; final ingestion validation remains mandatory.
Configuration returned by a strategy is cloned before request flags are applied.
Do not treat a caller-controlled `skip_resize` flag as moderation approval.

`UploadBatch` is not atomic. It returns the accepted prefix together with
`*host.BatchError`, whose `FailedIndex` identifies the first failed entry. The
HTTP API returns 207 for partial success, with the accepted files and index. Do
not retry already accepted entries. Complete failure preserves normal error
classification. Pagination is bounded to 100, invalid values are rejected and
list requests avoid the unused COUNT. File-type filtering uses stored types,
not a numeric string as a MIME prefix.

`SetPrimary` no longer moves files between objects. Perform an explicit,
separately authorized domain rebinding operation when that is truly required.

## Local filesystem and TUS profile

Local operations use `os.Root` to confine filesystem access. Session IDs must be
canonical nonzero UUIDs. Local TUS uses a bounded set of permanent interprocess
advisory lock files, checks length before writing and atomically replaces metadata.
Never unlink `.locks` while a process can use the store. This is a local-filesystem
profile; NFS/distributed filesystems and unsupported operating systems are not
claimed supported. Windows locking is included but was only cross-compiled in
this execution environment, not run on Windows.

The S3 transport remains an explicit fixed-chunk multipart profile, not arbitrary
PATCH-size buffering. Read the advertised `Upload-Chunk-Size`; intermediate
bodies must match it and only the final body can be smaller. Local TUS is not
subject to that S3 constraint. HEAD responses carry `Cache-Control: no-store`.
Configure Fiber body limits, reverse-proxy limits, CORS and timeouts to accommodate
the selected chunk size while bounding per-request memory. This candidate does
not remove the full-body buffering in the Fiber PATCH adapter.

## Validation and scanning

Limits are installed before image-header parsing. Header inspection has its own
bounded replay budget. Unknown image dimensions are not treated as proof of
valid image structure. The finalizer uses an exact-size reader that errors before
storage commit on truncated or oversized streams. Extension selection is stable
across operating systems, and original-mode configuration rejects types the
finalizer cannot store.

`UploadValidator` remains a metadata/first-512-bytes hook, not an antivirus API.
Stored uploads also pass through header validation. `OriginalRuntimeDeps` now
accepts an optional `ContentScanner`. It receives the complete private, bounded
spooled artifact; the same bytes are then published. The implementation must read
and validate the content and honor cancellation. Budget private temporary disk
space and concurrency when enabling this feature. Nil means no content scanner,
not a safety approval. No antivirus engine is bundled.

Final MIME/checksum validation does not perform moderation or make untrusted
content safe to render. Continue to own bucket access policy, public-delivery
headers, authentication, moderation and backup/restore in the embedding host.

## Verification before release

Run the declared toolchain and repository gates, then the added lifecycle tests:

```sh
make check
make test-surface test-surface-integration externalconsumer-local
# Point these at isolated test infrastructure, never production storage.
export TEST_PSQL_ADDRESS_LOCAL=127.0.0.1 TEST_PSQL_PORT_LOCAL=5432
export TEST_S3_ENDPOINT=http://127.0.0.1:9000
go test -race -tags=integration ./domain/files/repositories/filerepo -count=1
make test-tus-postgres-integration test-source-url-s3-integration
make test-portable-media-e2e test-originals-integration
make anonymous-source
```

The added workflow pins Go 1.27.1 and golangci-lint 2.14.0, separates build, lint
and integration jobs with bounded timeouts, and starts an isolated MinIO fixture.
Feature branches use the pull-request trigger; master/release pushes and manual
runs retain their triggers. Its image
is a pinned historical test fixture, not a production deployment recommendation.
A clean local replace probe still does not prove a published tag is anonymously
installable. Run the published-consumer gates for the actual candidate tag after
publication, and test consuming applications before rollout.
