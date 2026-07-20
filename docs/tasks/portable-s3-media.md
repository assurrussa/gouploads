# Portable S3 media contract

Status: implemented and locally verified; tag publication and clean published
consumer verification remain release actions.

## Purpose

gouploads must expose a provider-neutral S3 media contract through its stable
host facade. Hosts keep TUS and media processing in the write path while public
reads go directly to a canonical delivery origin.

The default topology uses one bucket with private staging/v1 objects and public
media/v1 objects. A separate staging bucket is optional. Public access is
controlled only by bucket policy; object ACLs are never sent.

## Stable configuration

Add these public contracts:

- StorageConfig.Public StoragePublicConfig;
- StoragePublicConfig with BaseURL and Prefix;
- StorageS3Config.StagingBucket;
- StorageTusConfig.StagingPrefix;
- FilePreset.ChecksumSHA256;
- a narrow StorageContractChecker plus a structured report.

Public.BaseURL is the complete delivery prefix. Production uses
https://media.spplab.ru and local path-style MinIO can use
https://ceph.localhost/my-bucket. Public.Prefix defaults to media/v1.
StagingPrefix defaults to staging/v1/tus. Source URL TTL defaults to 15 minutes.

Remove S3.Host, SourceHost, ACL, TransformHost, and DisableSSL. Keep endpoint,
region, public bucket, optional staging bucket, credentials/session token,
path-style mode, source TTL, timeout, and retries. MaxRetries must configure the
AWS SDK retryer. Do not add a provider enum or provider presets.

FilesBaseURL returns the delivery base and FilesBucket returns an empty string.
URL composition follows: an empty key returns an empty string, an absolute
external URL is unchanged, and a relative key becomes BaseURL plus the key.

## Object keys and lifecycle

Keys are created only by the server:

    staging/v1/tus/<session-uuid>/source.<safe-ext>
    media/v1/<object-type>/<object-id>/<file-slug>/<preset>.<safe-ext>

file-slug is the existing file-record UUID. A retry reuses its deterministic
key. A real replacement or reprocessing operation creates a new record and
therefore a new key. Original filenames remain metadata only.

Staging objects use Cache-Control: private,no-store. Final objects use their
validated content type and Cache-Control: public,max-age=31536000,immutable.
Every final artifact stores SHA-256 metadata in the database. Managed records
persist only folder_path plus filename and preset relativePath; provider URLs
are not persisted.

Locations are hidden for queued, processing, and failed records. Canonical
absolute URLs are composed at the HTTP/WebSocket edge only for completed
records. Presigned source GET uses the staging bucket and configured S3 endpoint
directly. Proxy host rewriting is not supported.

## Artifact finalization

Keep the existing media-resizer webhook wire contract with signed artifact
URLs. Artifact GET requests send no secret headers, never log signed queries,
allow only the configured media-resizer origin, validate status/size/type/MIME,
and stream into multipart S3 storage with bounded memory while calculating size
and SHA-256. HTTP is accepted only for an explicitly configured internal
origin; external production origins require HTTPS.

All final artifacts are written idempotently first. File metadata and
after-jobs are committed atomically. Staging deletion happens only after the DB
commit through an idempotent retried outbox job. Partial final keys from a
permanent finalization failure use the same cleanup path.

Keep the existing host.Storage interface for local-mode and goadmin
compatibility. Public/staging bucket routing stays inside the S3 adapter. Rename
the internal generic ceph implementation to s3store; it does not become a
supported import.

## Contract checker

Expose a checker suitable for an explicit host CLI. It creates unique probe
keys, never calls ListBucket, and always attempts cleanup. The report covers
staging put/head, denied anonymous staging GET, successful presigned staging
GET with SHA-256 verification, final put, public TLS HEAD/GET/Range with body,
content type and immutable cache checks, authenticated delete, and subsequent
public unavailability.

## Verification and release

Unit and contract tests cover config validation, URL composition, key safety,
no ACL, bucket routing, retries, cache headers, checksums, hidden locations and
signed-query redaction. Mandatory E2E uses MinIO, PostgreSQL and a stub artifact
server for one- and two-bucket modes, image/video/PDF, retry and partial
failure. Missing S3 is a test failure, not a skipped test.

Update README, host integration, project map, external-consumer probe and
RELEASING.md. Supported imports remain only /host and /hosttest. The next
candidate is v0.10.0-alpha.4; published readiness requires the full pre-tag gate
followed by a clean consumer resolving the pushed tag without a local replace.
