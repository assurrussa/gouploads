# Recovery preview: required evidence contract

Status: recovery dependency design; no recovery-preview API or mutation is introduced.
The opt-in [file job evidence implementation](file-job-evidence.md) now supplies
partial producer provenance and read-only queue observations. It does not satisfy
complete coverage, success retention, operation fencing or storage/remote evidence.
The original evidence inventory below describes the baseline reviewed for this design.
Checked against merged GoUploads `314ea8e9327b95ed05a5d26527d1fdbf7dee11f0`
and its pinned `github.com/assurrussa/outbox` / PostgreSQL backend `v0.12.0`.

A useful mutation preview cannot yet be implemented through the supported host
contracts. `host.DiagnoseFile` already reports the available lifecycle facts;
wrapping those facts in a preview that always says blocked would add no recovery
capability. Keep using diagnostics while the dependency below is implemented
and reviewed. In particular, **failed upload metadata is never permission to
retry, and pending deletion metadata is never permission to delete**.

## Verified limits

| Source | Actual contract | What it cannot prove |
| --- | --- | --- |
| `host.UploadOutbox` / upload producers | `Put` returns a JobID; `Service.uploadFile` discards it for original/media enqueue | Which active/failed job belongs to a requested FileID |
| Outbox `v0.12.0` `JobsRepository` | Fenced claim/extend/release/ack/reschedule, using lease token and time | Read-only FileID lookup or a recovery claim |
| PG `jobsrepo.GetByID` | Reads an active job by a known JobID | Binding to FileID, prior success, or a retained terminal outcome when absent |
| PG `jobsfailedrepo.GetByID` | Reads by failed-row ID; failed row has a separate original `JobID` | Lookup by original JobID through that method, complete per-file failure history |
| Outbox success acknowledgment | `ackBatch` calls `DeleteJobWithLease` | A missing active row means success; it might have failed, expired or been removed |
| Queue/capability statistics | Counts available/processing jobs by name/version | Per-file state, absence of a competing operation, worker or external-I/O quiescence |
| `upload_finalizations` | Retained key/binding/FileID handoff tombstone; no job columns | Storage finalization outcome, a particular job, or request commit certainty |
| `file_deletions` | Approved artifact payload and nullable completion timestamp | Worker lease/failure history, no operation in flight, or independent storage verification |
| `GetFileLifecycle` | One read-only DB snapshot, no payload or storage reads | Operation generation/revision, remote state, uncertain-commit reconciliation |

Repository evidence: [upload producer](../domain/files/service/uploadservice/service.go),
[finalization repository](../domain/files/repositories/filerepo/lifecycle.go),
[lifecycle projection](../domain/files/repositories/filerepo/diagnostics.go),
[original finalizer](../domain/files/usecases/command/upload_file/original.go),
[deletion worker](../domain/files/usecases/command/delete_file/usecase.go),
[lifecycle migration](../host/migrations/20260930120000_upload_lifecycle_safety.sql).
The Outbox facts above are from the exact module versions in [go.mod](../go.mod),
not its changing development branch.

The media dispatch continuation's `JobID` is the **remote resizer job ID**, not
an Outbox JobID. The current callback `ExternalID` is read as FileID by the
listener; it does not identify either queue or remote job. Do not infer those
job identities from FileID, conflate them, or scan arbitrary JSON payloads
or paths to manufacture a relation. Staging-only deletion jobs with `FileID=0`
are outside FileID recovery and must not be attached to another file.

The original finalizer keeps deterministic staging/final artifacts after an
uncertain metadata commit and never schedules final-key deletion on that error.
Deletion freezes its plan before storage I/O, completes it transactionally
with after-jobs, and keeps completed plans idempotent. A preview must preserve
these behaviors rather than call existing handlers as read probes.

## Minimal dependency before implementation

Provide one optional, read-only, host-owned **recovery evidence reader**, exposed
through a future narrow `host` contract. It accepts the already authorized
positive FileID and the requested operation (`original finalization`, `media
admission`, `media finalization`, or `existing deletion plan`). It returns only
normalized evidence and opaque revision references. Construction must not start
workers, mount a route, claim a lease, enqueue work, or access storage implicitly.
This is a proposed contract, not an exported interface in this change.

Its irreducible guarantees are:

1. **Durable association and coverage.** Explicit producer-created FileID,
   operation/generation, job identity and name/schema-version association,
   committed atomically with the enqueue through the same transaction context.
   Each continuation/new job must extend the same operation association. The
   reader states whether that operation's set of links is complete, incomplete,
   historical/unmapped or unavailable; an empty set never means no jobs without
   explicit complete coverage. Existing uploads stay unmapped until an authorized
   provenance-preserving backfill is designed. Do not guess links from payloads.
2. **Read by those identities.** Observe each mapped job as available/delayed,
   leased, terminal-failed, missing-after-known-retention, or unavailable/unknown.
   Include observation time, attempt/capability identity and an opaque version
   reference. Do not expose lease tokens, job payloads, failure text or credentials.
   Read active and failed evidence coherently where the backend permits; otherwise
   return unknown for a transition race. Absence is unknown without a retained
   outcome. A terminal-success claim requires explicit retained acknowledgment
   evidence or independently reconciled operation evidence, never an absent row.
3. **Operation evidence.** Identify the current lifecycle generation, deletion
   plan/tombstone state and whether the requested operation's outcome is confirmed
   or uncertain. Do not infer commit certainty from a timeout or from job state.
   A lease expiry is not proof that old external I/O has stopped. Keep these
   unknown until an authoritative operation/fencing contract supplies evidence.

This requires producer association persistence plus a backend/host adapter;
adding `GetJob(FileID)` to a facade without those guarantees is insufficient.
No Outbox dependency upgrade, association schema, backfill or retention change
is authorized by this design-only PR. A next implementation PR must first pin
and prove the adapter against its selected supported Outbox contracts.

Storage/source validation and remote media admission remain separate explicit
read-only prerequisites. The evidence reader does not solve them. The future
preview may safely return unknown while these are unavailable; it must not
create a fake positive result merely because job evidence became readable.

## Future preview outcome rules and acceptance cases

The following are contract acceptance cases for a future implementation,
**not executed runtime tests or current API behavior**. Outcome and reasons must
be deterministic enums. Evaluate definitive prohibitions/conflicts first, then
missing evidence. Positive results require every applicable prerequisite.

| Evidence/trigger | Outcome | Required interpretation |
| --- | --- | --- |
| Lifecycle reader fails, missing table, unknown metadata | unknown | Source unavailable/unsupported; never reinterpret as missing or retryable |
| File missing with retained finalization tombstone | blocked | No recreation/retry; preserve binding and no-resurrection guarantee |
| File absent without tombstone, job/storage history incomplete | unknown | No proof of safe recreation or orphan deletion |
| Upload completed; no incompatible plan | blocked | No upload retry needed; completion does not prove storage health |
| Deletion plan completed, file absent or soft-deleted | blocked | No repeated recovery delete; preserve completion tombstone |
| Upload retry requested while deletion is pending/complete | blocked | Conflicting lifecycle; do not revive or remove finalization evidence |
| Present file with completed deletion, incompatible generation or contradictory durable states | blocked | Inconsistent/conflicting evidence requires reconciliation; do not choose a winner |
| Any mapped job available, delayed or actively leased | blocked | Normal worker owns execution; do not enqueue another operation |
| Lease expired but external I/O/quiescence not proved | unknown | Lease time alone cannot authorize takeover |
| Upload marked failed but job links unavailable/incomplete | unknown | File metadata is not a queue/DLQ lookup |
| Terminal failure with complete job links, uncertain operation commit | unknown | Reconcile durable outcome before retry or cleanup |
| Terminal failure with confirmed DB outcome, storage/source unknown | unknown | Cannot validate staging bytes, deterministic final artifacts or scanner policy |
| Media job has unknown remote admission/identity | unknown | No duplicate POST, forged remote identity or reset deadline |
| Pending deletion with complete terminal-failed job links but plan/artifact approval unknown | unknown | Do not rebuild approval from a path or missing row |
| All operation-specific evidence proved and reviewed command contract available | actionable | Eligible to request that fenced command; never `safeToRetry`/`safeToDelete` or an automatic action |

For an actionable **original-finalization** candidate, require a present queued
file of the same generation, no deletion conflict, complete terminal-failed job
coverage and worker quiescence, reconciled commit outcome, validated retained
staging bytes/content policy and deterministic-artifact state, and a reviewed
original retry command. An arbitrary failed metadata row is insufficient: the
existing original handler itself requires queued status and does not define a
failed-to-queued recovery transition.

For an actionable **deletion** candidate, require the same already-approved
pending durable plan, proven artifact ownership/scope, complete terminal-failed
job coverage, worker/external-I/O quiescence, reconciled completion/after-job
commit and a reviewed command that uses that plan. Never synthesize a plan or
infer orphan ownership from a missing FileID. Media actions additionally require
reconciled remote admission/job identity and existing idempotency/deadline state.

New contract tests must exercise every row, mixed queued/leased/terminal jobs,
active-to-DLQ observation races, truncated link sets, expired history, cancellation,
wrapped source errors, and repeated/stale previews. Verify that every adverse
state blocks or stays unknown and that no reader call performs a mutation.

## Fenced command boundary and sanitization

The host authorizes the FileID/operation and historical ownership before preview.
Only its protected operator flow can request a future action. A preview is an
observation and never a mutation token: even an actionable result must be
revalidated atomically by the separately reviewed command against generation,
plan/tombstone, job ownership and commit evidence. Storage/remote I/O needs its
own fencing/reservation/idempotency protocol and fresh reconciliation. Keep
finalization tombstones and minimal deletion completion markers through retention.

Responses contain FileID, operation, outcome, enum reasons and named missing
prerequisites only. Keep paths, URLs, keys, identities, callbacks, raw errors,
DLQ reasons, leases and remote/job payloads out of responses and unfiltered logs.
No route, retry/delete method, worker registration or runtime rewrite is part of
this stage. Existing `host.DiagnoseFile` remains the shipped read-only capability.
