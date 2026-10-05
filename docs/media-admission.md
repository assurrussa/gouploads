# Durable media admission and client reconciliation

`media_resizer` remains explicit opt-in. Audio stays `original_only`.

## Identity and retry contract

One GoUploads file record is one logical media attempt. Its decimal file ID is
sent as `idempotency_key`; ordinary outbox retries keep it unchanged, even when
the private S3 source is presigned again. Do not recycle file IDs across hosts
sharing an API token scope. Use a distinct resizer token scope per upload database.

A 202 response acknowledges admission, not processing completion. The client
validates the returned nonzero logical job ID and persists a continuation in the
existing `send_resize_file` outbox. Continuations carry `jobId` and `pollDeadline`;
they only GET the job with the same configured endpoint/token, never POST again.
Pending work schedules another durable check in 30 seconds. Callback success may
win the race; the completed file then stops polling.

If the POST response or continuation insertion is lost, retry the original
outbox payload with the same key. A re-signed URL does not refresh the server's
original admitted payload. The original URL must remain valid through source
fetch; make the source TTL cover queue delay. A conflicting body returns 409, is marked permanent in the client outbox, and
must be investigated. Retry-After on transient admission failures is preserved
through the outbox retry disposition. Never fix a conflict/timeout/unknown outcome by silently
changing the key or presigned source resource.

## Terminal state and uncertainty

The resizer currently sends successful completion callbacks only. Durable GET
reconciliation therefore handles both missed success callbacks and terminal
failure. A done response feeds the existing artifact persistence job. A failed
response updates the file's uploader status under the same row lock as final
storage, without deleting source bytes or downgrading an already completed file.
Duplicate callbacks/polls may enqueue duplicate finalizers; finalization remains
row-locked and idempotent. This is not an exactly-once processing guarantee.

GET job ID and idempotency key must both match the persisted continuation. Failed
jobs need not retain metadata. Callback `attempt` is not a generation fence.
Unknown/reconciliation-required status, missing/expired artifacts (404), invalid
identity, and a seven-day client polling deadline return explicit errors to the
outbox. They do not mark the file failed, POST another job, or rotate a key.
The outbox's normal retry/dead-letter policy makes these visible for operators.
Investigate the resizer admission/queue and file state before taking action.

Intentional reprocessing is a fresh upload with a fresh file record/key. There is
no same-file reprocess API; changing only a key without persistent generation
fencing would allow stale callbacks to overwrite a newer attempt. A failed
attempt retains its staging source, subject to the host's normal retention rules.

## Wiring and coordinated cutover

The configured DI provider wires submission, durable outbox continuation, and
completion handler together. Legacy, unsupported deep-package constructors must now provide
the outbox putter and result handler to `sendresizefile.NewOptions`; these are
required, not an optional callback-only mode. Supported hosts continue using
`host.BuildOutboxJobs` and the existing `send_resize_file` registration.

Deploy all capable readers/workers before allowing continuation-producing
clients. Old workers do not understand `jobId` and must never consume new
continuations. Their required source-path validation fails closed because a
continuation deliberately has no source URL. Quiesce/drain or stop old POST producers and old client workers;
then switch all clients, ops writers, and workers as one coordinated cutover.
Do not maintain a long-lived mixed processing framework.

On the resizer, apply migration `0009_media_admissions.sql` after existing
migrations, verify every process uses admission-capable code and consistent
policy, then explicitly enable admission only after isolated acceptance passes.
The server default remains disabled. This change does not deploy, change
production flags, rotate credentials, or grant additional access. Preserve
original-only audio workers independently.

Acceptance must exercise real image/video (including previews), authenticated
POST and GET, callbacks and final storage; lost response/same-key replay,
re-signed URLs, conflicting payloads, terminal failure, and a fresh upload as a
new logical attempt. Unit fakes alone are not live-chain evidence.
