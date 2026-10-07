# Read-only file diagnostics and recovery roadmap

`host.DiagnoseFile(ctx, reader, fileID)` combines lifecycle evidence for one
positive FileID. `*host.FileRepo` implements `host.FileDiagnosticReader` with
`GetFileLifecycle`: one SELECT observes the file (including soft-deleted rows),
finalization handoff tombstone and deletion plan completion in the same database
snapshot. The query does not lock rows or decode deletion payloads. Apply the
existing lifecycle migration before using it; a missing table is an unavailable
source, never evidence that a file is missing.

The host must authorize access to the FileID, including tenant/object ownership,
**before** invoking the diagnostic. No HTTP route is installed. A host may wrap
the reader to enforce its own authorization. Do not use file existence from the
diagnostic as an authorization check. The module does not know the host's tenant
policy. A retained plan or tombstone can outlive the authorized file row; hosts
need their own ownership evidence for historical operator access.

```go
// After host authorization succeeds for this FileID:
diagnosis, err := host.DiagnoseFile(ctx, runtime.Files, fileID)
if err != nil {
    // errors.Is(err, host.ErrDiagnosticUnavailable) distinguishes source failure.
    // Context cancellation/deadline and invalid IDs retain their safe sentinels.
    return err
}
// Return only this path-free result through a host-protected operator endpoint.
return diagnosis
```

This embedding fragment is not an endpoint implementation. An executable example
is in `host/diagnostics_test.go`.

| Field | States | Evidence and limit |
| --- | --- | --- |
| `file` | `present`, `deleted`, `missing`, `unavailable` | Row exists, soft-deleted row, absent row, or failed read. Missing does not prove storage absence. |
| `upload` | `queued`, `processing`, `completed`, `failed`, `unknown`, `unavailable` | Persisted uploader status. Empty status plus nonempty preset map follows `File.IsUploadCompleted`; other empty/future values are unknown. Missing file means unknown. |
| `finalization` | `recorded`, `none`, `unavailable` | `upload_finalizations` has a handoff tombstone. Recorded means enqueue handoff, not successful storage processing. None also occurs for non-keyed/legacy uploads. |
| `deletion` | `deleting`, `completed`, `none`, `unavailable` | A durable pending/completed `file_deletions` plan. Deleting does not prove a worker is running. No persisted deletion-failure state exists here. |
| `job` | `unavailable` | `UploadOutbox` exposes only `Put`; it has no stable FileID/job-state lookup. Upload status is never presented as queue/lease status. |

The fields remain independent: a missing row may still have a recorded
finalization and completed or pending deletion plan. No aggregate state discards
this evidence. Results are observations at read time and may become stale
immediately; they cannot authorize a mutation. Storage existence, checksum,
worker liveness and media-resizer remote state are not queried. A finalization
key/session without an assigned FileID is outside this API.

The result contains only the requested ID and normalized enum values. It omits
paths, URLs, object keys, names, identity metadata, callback/job payloads,
finalization keys and bindings. Unrecognized status strings are not echoed.
`ErrDiagnosticUnavailable` suppresses raw driver errors; all source-backed fields
become unavailable on failure, even after a partial scan. Job remains unavailable
on success. Do not add raw errors to host responses or unfiltered logs.

## Verification

After acquiring the heavy test lane, run `make test-diagnostics-postgres-integration`
against the authorized test PostgreSQL service. The target uses the existing
integration address/port overrides. It exercises the real embedded migrations,
SQL projection, JSON operators and pgx decoding, including absent files with
retained plans/multiple keys, soft deletion, null/empty/nonobject metadata,
recognized/future statuses and missing lifecycle tables.

Each fixture creates a UUID-named schema, verifies its search path and drops only
that schema after closing its scoped pool. It never resets a shared database,
truncates tables or runs migration downs. No live storage or worker is invoked.

## Sequenced follow-up PRs

1. **Recovery contracts and preview first.** The current contract gap and
   proposed minimum are documented in [recovery preview dependency design](recovery-preview-contract.md).
   No preview API is shipped until that dependency exists. Define a host-supplied, authorized,
   read-only job lookup using the outbox's verified public contracts. Establish
   durable FileID-to-job correlation for original, media dispatch/continuation
   and deletion jobs, plus retention semantics. Never search arbitrary payloads
   or infer job IDs from FileID. Specify pending/leased/retryable/terminal/unknown
   evidence, observed revision/lease and host-scoped operator audit identity.
   Design a path-free recovery preview with explicit conflicts and unavailable
   sources. New contracts and their tests must precede mutation implementation.
2. **Explicit recovery command.** After preview review, implement bounded,
   host-authorized retry/reconcile actions with transactional revalidation and
   fencing against active workers, deletion and uncertain commits. Preserve
   finalization binding/idempotency, validate staging availability and content
   policy, and coordinate media remote-job identity to avoid duplicate dispatch.
   A failed status alone is insufficient. Test repeated, concurrent, canceled,
   stale-preview and out-of-order operations. No automatic retry/delete is part
   of diagnostics; never resurrect a gone finalization tombstone.
3. **Deletion retention and reconciler.** Introduce separately reviewed retention
   for completed `file_deletions` payloads and pending-plan reconciliation.
   Preserve a minimal completion tombstone so late/replayed delete jobs cannot
   rebuild a plan from an absent record. Never expire `upload_finalizations`
   with file retention: their key/binding/FileID tombstones prevent resurrection.
   Before compacting deletion payloads, specify handler behavior, migration and
   coordinated worker rollout. Reconciliation needs bounded pagination,
   claim/lease fencing, backoff, host audit and proof of completed artifact
   deletion; a missing job or stale timestamp does not justify deleting data.
4. **Storage injection.** Optional local-driver injection is implemented through
   `OriginalRuntimeDeps.Storage`; see the [ownership and instrumentation contract](standalone-uploads.md#caller-owned-storage).
   Defaults and caller ownership are preserved. Local TUS retains its config-built
   protocol spool and hands completed bytes to the supplied persistence store.
   S3 overrides remain blocked on a compatible TUS dependency contract: prove
   multipart/session and key-handoff compatibility before adding that override.
   Keep the existing lifecycle path and test both default adapters and supplied
   wrappers when extending this contract.
5. **Measure streaming and transaction duration.** Benchmark representative
   original sizes and media fanout, observing peak memory, spool bytes, I/O,
   transaction/lock durations and failure windows. Use bounded fixtures owned by
   the task and acquire the heavy lane before build/race/container work. Move
   external I/O outside long transactions only with a proven reservation/fence,
   immutable publication and short commit protocol; retain checksum/scanner
   validation, rollback and uncertain-commit recovery. Choose streaming changes
   from measurements, with before/after correctness and resource evidence.

Each follow-up is a separate bounded review. No release, deployment or registry
publication is implied by this roadmap.
