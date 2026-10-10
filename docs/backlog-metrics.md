# Host-owned backlog metrics

Use existing read-only snapshots for backlog gauges. A counter of callbacks,
successful uploads or completed jobs cannot reconstruct current backlog after
restarts, retries, duplicate callbacks or activity from another process.

The [external consumer example](../reference/externalconsumer/backlog_example_test.go)
shows one host-owned callback that receives aggregate counts. It adds no runtime
API, monitoring dependency, HTTP route or automatic polling. Pass the existing
Outbox service (`GetQueueStats`) and `runtime.Files` into the adapter; replace the
example's printing callback with gauge setters in the host's metrics system.
The readers and callback must be non-nil; the callback must be fast and safe for
the host's calling concurrency. The Outbox service needs a `JobsStatRepository`:
standard repositories are detected automatically, or the host can supply one
with `outbox.WithJobsStatRepo`. Missing configuration is a collection error.

## What each measurement means

- Jobs: `QueueStats.Total` counts all active Outbox rows in the configured queue
  repository, including unrelated host jobs and unsupported capabilities. It
  includes delayed retries and excludes acknowledged/deleted jobs and the
  separate dead-letter queue.
- Upload jobs: sum `ByCapability.Total` for `finalize_original_file`,
  `send_resize_file`, `listen_resize_file` and `upload_file_persist`, across all
  schema versions. This fixed set describes GoUploads' current processing jobs.
  It is not the number of unique files or active TUS sessions. One file can have
  multiple jobs and a stalled file can have none; do not label it "uploads".
- Pending deletions: `InspectDeletionRetention(..., nil).Snapshot.PendingCount`
  counts durable deletion plans with no completion timestamp. It is independent
  of the number of `deleted_file` queue rows. Completed retained tombstones are
  not pending backlog, and missing job evidence never proves deletion success.

`Available` and `Processing` and the capability groups are already available in
Outbox's snapshot if the host needs separate gauges. Use the queue observation
time minus a capability group's nonzero `OldestAvailableAt` for oldest ready-job age. Deletion age
uses the deletion snapshot's observation time and non-nil
`OldestPendingCreatedAt`; absent age is unknown, not zero. See
[deletion-retention evidence](deletion-retention-inventory.md) for unknown ages.

## Collection and failure contract

Authorize the full scope of both repositories before collecting. The standard
deletion reader covers its entire database scope; it is not a per-tenant view.
The queries scan their tables. The host owns scrape cadence, deadlines, access
control and cost limits. Keep collection outside business transactions and
upload request handlers. The two reads are separate observations, not one atomic
cross-table snapshot; retain both timestamps when tracking freshness.

The example emits only after both sources succeed. On either error, report an
unsuccessful collection and expose last-success time/staleness through the host's
metrics system. Never replace unavailable data with zero or continue presenting
an old value as freshly observed. A successful empty snapshot does emit zero.
The host can instead collect each source independently with separate freshness
and failure state. This adapter has no default polling interval or thresholds.

Use fixed metric names and bounded, host-approved dimensions. Do not export
FileIDs, actor/object identities, paths, signed URLs, raw errors or payloads.
The example emits aggregate values without labels. Lifecycle logs and Outbox's
optional outcome observer can complement these gauges; neither replaces them.

These examples use the current GoUploads source's merged-but-unreleased
deletion inventory. It is not included in GoUploads `v0.12.0`. They require no
Outbox version change: `Service.GetQueueStats` and capability snapshots already
exist in the Outbox `v0.12.0` dependency declared by this checkout. No release, consumer
upgrade or runtime wiring is performed by this documentation change.

## Verification

```sh
go test ./reference/externalconsumer -run 'Example_backlogMetrics|TestBacklogExample' -count=1
make externalconsumer-local
git diff --check
```

The examples test independent timestamps, multiple schema versions, unrelated
jobs, unavailable sources and a genuinely empty snapshot. They are not live
database or production scrape evidence.
