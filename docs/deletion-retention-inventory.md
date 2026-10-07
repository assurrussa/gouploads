# Read-only deletion-retention inventory

`host.InspectDeletionRetention(ctx, reader, completedBefore)` reads aggregate
retained `file_deletions` evidence. `*host.FileRepo` satisfies
`host.DeletionRetentionReader` with one SQL statement. Its scope is **all plans
in the repository's database scope**, including pending plans and completed
idempotency tombstones. The host must authorize that entire scope before calling,
or provide an authorized, scoped reader. No route or schedule is installed.
Use an administrative context with an appropriate deadline: the aggregate scans
the table and computes JSON text lengths; it makes no bounded-cost claim.

```go
// Authorize the inventory's whole database/tenant scope first.
// nil means no cutoff and no projection. There is no default retention age.
inventory, err := host.InspectDeletionRetention(ctx, runtime.Files, nil)
if err != nil {
    // Evidence is unavailable; never interpret this as an empty inventory.
    return err
}
// inventory.Source == host.DiagnosticPresent, even for an empty table.
counts := inventory.Snapshot
_ = counts.TotalCount
```

A caller-supplied, nonzero cutoff in UTC years 1 through 9999 enables a what-if
projection. Input is copied and normalized to UTC; the caller's value is not
mutated. `completed_at < completedBefore` is strict: equality is excluded.
Nanosecond cutoffs retain exact comparison semantics: the SQL parameter is rounded
up to the next representable microsecond when necessary, while the projection
reports the original UTC cutoff. Since stored timestamps are microsecond-aligned,
this includes a row at T for a cutoff T+1ns, and excludes it for T or T-1ns. Only
completed rows with known ages are counted. Malformed envelopes and unrecognized
bindings can still appear in the projection: it describes age, **not eligibility
for compaction or deletion**. A nil projection differs from a requested projection
with zero matching rows. No duration is inferred from the cutoff.

## Evidence fields

- `totalCount`, `pendingCount`, `completedCount`: row counts, with completion
  determined solely by `completed_at IS NOT NULL`. Pending plus completed equals
  total, independently of payload diagnostics.
- `malformedEnvelopeCount`: root/file is not an object, or `file.id` is not a
  JSON number whose canonical text equals the row's positive FileID. SQL JSONB already
  excludes invalid JSON syntax. This checks a minimal envelope, not every field
  accepted by the current Go decoder.
- `unrecognizedBindingCount`: among recognized envelopes, `objectType` is not a
  string matching the current public type syntax, or `objectId` is not a canonical
  positive int64 JSON number. Custom host object types are recognized; this is
  not an enum allowlist, ownership authorization or full-plan validation.
- `unknownAgeCount`: `created_at` is nonfinite, before UTC year 1, or later than the statement's
  observation time; or a non-null `completed_at` is nonfinite, later than that
  time, or earlier than `created_at`. These rows remain in the main totals.
- `observedAt`: database `statement_timestamp()`; `oldestPendingCreatedAt` is
  the minimum **created_at** of known-age pending rows. `oldestCompletedAt` is
  the minimum **completed_at** of known-age completed rows. Null means no known
  timestamp in that group, not age zero. Subtract these timestamps from
  `observedAt` to display age; do not substitute created time for completion.
- `payloadJsonTextBytesEstimate` and its pending/completed/projection variants:
  sums of `octet_length(payload::text)`, the database's serialized JSON text
  representation. These are **not PostgreSQL physical disk usage or predicted
  disk savings**: they exclude row/index overhead and do not model compression,
  TOAST, vacuum or the size of a replacement tombstone.

The malformed/unrecognized/unknown-age counts may overlap age groups. They do
not certify recoverability, plan decoder compatibility, actor policy, holds,
job state, storage existence, external-I/O quiescence or compaction eligibility.
The inventory returns no per-row IDs, object/actor identity values, paths,
keys or payloads. A successful empty read has a present snapshot with zero
counts. SQL/scan failures yield `DiagnosticUnavailable`, no partial snapshot and
`ErrDiagnosticUnavailable`; context cancellation/deadline remain detectable
without leaking database errors. Missing lifecycle tables are unavailable.

## Compatibility and next decisions

This change does not compact, expire, delete or reconcile anything. Existing
cleaner `Minutes` concerns soft-deleted file rows; it is not a deletion-plan
retention policy. Runtime defaults, pending plans, deletion completion behavior,
finalization tombstones and TUS object-retention guards are unchanged.

The current deletion reader decodes completed payloads before recognizing the
completed no-op. A minimal completed payload containing `file.id`,
`file.objectType` and `file.objectId` remains decodable and preserves the current
object-binding guard. Tests demonstrate a same-binding retry is a no-op and a
wrong binding is rejected without storage calls, events or after-jobs. They are
compatibility fixtures, not a complete authorization/audit retention policy.
Empty/null payloads or an ID-only tombstone cannot be assumed equivalent.

Before a separate compaction implementation, choose completed-payload retention
age, historical actor/audit fields, holds/exemptions, enablement, observability
and rollout policy. Preserve durable completed identity/binding tombstones and
all finalization tombstones; pending plans remain intact. Introduce a versioned
payload compatibility contract if the chosen policy needs a different shape.
Pending recovery/reconciliation still requires the durable job associations,
complete job-state coverage and external-I/O/uncertain-commit fencing described
in [the recovery design](recovery-preview-contract.md). The broader ordered work
remains in [the diagnostics roadmap](file-diagnostics.md).
