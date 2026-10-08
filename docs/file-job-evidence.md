# File operation and Outbox job evidence

Status: source addition, unreleased. This is diagnostics and producer provenance,
not recovery permission. It targets the exact Outbox core and PostgreSQL backend
versions pinned in `go.mod` (`v0.12.0`); no dependency upgrade is required.

## Explicit opt-in

Construct `host.NewPostgresFileJobOutbox(database)` with the pinned
`*pgsqlclient.Client`. Supply this producer as the existing Outbox dependency
to the original runtime or to all explicitly wired upload/media/deletion
constructors. Keep the same database and pinned transaction context for file
metadata and queue workers. Register and run workers separately as before.
Construction performs no I/O and neither applies migrations nor starts workers.

This is intentionally a concrete PostgreSQL adapter, not a wrapper around an
arbitrary `UploadOutbox.Put`. Merely passing the same context to an unknown queue
cannot prove atomicity. Existing custom queues retain their behavior and remain
unmapped unless they explicitly implement the producer provenance contract.
No supplied queue is silently replaced. Apply the new GoUploads migration and
the pinned Outbox migrations before opting in.

The supported producer paths carry the already loaded File, explicit operation
and actual returned queue JobID:
- Original finalization enqueue
- Initial media admission and each polling continuation
- Media finalization enqueue after a typed successful callback/poll result
- Explicit upload-service deletion requests

Remote resizer JobID and callback ExternalID remain separate identifiers.
No payload is parsed to derive a queue association. Staging-only FileID=0,
arbitrary after-jobs (including replacement deletion), the inbound callback queue,
older queue rows and third-party/direct producers are not silently attributed.
Legacy files without a generation remain unmapped. Newly produced continuations
for older files can add partial evidence; they do not backfill earlier jobs.

## Transaction and generation guarantees

The adapter uses pinned `transaction.Manager`, `jobsrepo.CreateJobVersioned`
and `pgsqlclient.Getx`. The latter routes through `storage.GetTx(ctx)`.
Association insertion uses that exact pgx transaction directly. Nested enqueue
inherits the metadata transaction. On association failure, the outer transaction
must fail; callers must propagate the error rather than swallow it.

The adapter locks and checks FileID, slug generation, object type, nullable object
ID and non-deleted state before enqueue. Each successful queue insertion records
those values, operation, actual JobID, job name and schema version together.
The links deliberately have no cascading foreign key to files or jobs: normal
acknowledgment deletes queue rows, and file deletion must not delete provenance.
No cleanup, retention default, compactor or backfill is introduced.

The adapter preserves existing Put/retry semantics. It does not deduplicate
different requests or suppress continuation jobs. Repeated puts produce distinct
retained links. Existing TUS finalization-key idempotency still owns upload
handoff; a replay returning the already-created File does not call the producer.
An outer rollback removes both queue row and association. An uncertain commit
returns an error, not an accepted JobID; do not compensate with another enqueue
or storage deletion. A nested Put result remains tentative until the enclosing
transaction commits. Read-back evidence does not establish storage/remote outcome.

## Read-only diagnostics

After authorizing the FileID and historical ownership, call
`host.InspectFileJobs(ctx, fileRepo, fileID, operation)` with one of:
`FileJobOriginalFinalization`, `FileJobMediaAdmission`,
`FileJobMediaFinalization`, or `FileJobDeletion`.
The existing `host.DiagnoseFile` remains a separate lifecycle-only observation.

`FileRepo.GetFileJobs` performs one bounded, non-locking PostgreSQL statement.
It joins persisted IDs to `jobs.id` and `jobs_failed.job_id`, never the separate
failed-row ID. A single statement snapshot avoids an active-to-DLQ transition
race. Missing tables/unsupported sources are unavailable, not empty history.

At most 100 links are returned newest-first in creation/ID order. This keeps
recent polling continuations visible even when a long admission has older links. The newest-100 window is a diagnostic bound, not storage retention or complete
historical coverage. All association rows remain retained; no automatic expiry
or cleanup policy is introduced. A 101st link sets
`Truncated`. Coverage is always `partial` for any observed links, and
`historical_unmapped` for an empty set; it is never complete. Missing arbitrary
after-jobs, old workers, callback producers or external producers cannot be ruled
out. No link deletion/backfill operation is provided.

Per-job states are available, delayed, leased, lease_expired, terminal_failed,
or unknown. Active/failed contradictions and capability mismatches are unknown.
Missing active AND failed rows are unknown: successful acknowledgment, manual
removal and expired history cannot be distinguished. No success ledger exists
in the pinned backend. A lease expiry says nothing about old external I/O.

The result includes server observation time, capability identity, active attempt
count when available, opaque generation/revision references and current-binding
status. It excludes payloads, paths, URLs, tokens, DLQ reason/exception text and
raw database errors. Outcome and worker quiescence remain explicitly unknown.
The references are observations, never recovery tokens or fencing authority.

This stage does not implement recovery preview/actions, retry/delete/reconcile,
remote admission reconciliation, source scanning, storage verification, operation
fencing or success retention. See [the recovery dependency contract](recovery-preview-contract.md).

## Validation

The change includes unit coverage for opt-in dispatch, legacy/staging exclusion,
error propagation, transaction-context reuse, link failure, uncertain commit,
bounded coverage and sanitized diagnostics. Owned-random-schema PostgreSQL tests
cover identical queue/link xmin, rollback/cancellation, link-schema failure,
read-only observation, active/failed original-ID lookup, unknown absence,
binding changes, retained links after file deletion, and truncation.
Run these against synthetic fixtures only. Required repository/public-surface
gates remain mandatory before publication; no CI trigger is added.
