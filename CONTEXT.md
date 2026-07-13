# Context

## Glossary

- **Host Surface**: The stable package path a host project may import when it
  embeds `gouploads`. Today that surface is `github.com/assurrussa/gouploads/host`.
- **External Consumer Manifest**: The machine-readable package list in
  `gouploads/reference/externalconsumer`. It is the source of truth for stable
  host imports.
- **Deep Upload Package**: A package under `domain/files/*`, `config`, `di`,
  `infrastructure/*`, or `shared/*`. These packages may remain public in Go's
  visibility model, but they are not the preferred host integration contract.
- **Host Adapter**: Application-owned code that maps local environment config,
  auth/session context, object policy, routes, and deployment wiring into the
  `gouploads/host` contracts.
- **Upload Projection**: File metadata owned by `gouploads` storage. Host object
  identifiers such as `object_type` and `object_id` attach that metadata to a
  host domain object without making the host own upload internals.
- **Resize Webhook**: Callback from the media processor into a host HTTP route.
  The library owns the canonical request contract; the host owns transport
  parsing, auth, and route policy.
- **TUS Fence**: Monotonic PostgreSQL session revision bound to a short lease.
  Only its current owner may advance the durable offset or finalize multipart
  state.
- **Quarantine Object**: Private S3 multipart object that is not public merely
  because TUS protocol finalization succeeded. Promotion follows host/CMS media
  validation.

## Rules

- Host projects should import `github.com/assurrussa/gouploads/host`, not
  `domain/files/*`, `shared/*`, `config`, or `di` directly.
- `shared/*` and `di` may keep their physical package names for internal
  compatibility, but stable consumer access must go through the Host Surface.
- New host runtime imports must pass `cmd/importpolicy` without transitional
  deep-import allowances.
- Local reusable-boundary readiness means `make release-readiness` passes.
  Published reusable-boundary readiness additionally requires
  `make externalconsumer-published VERSION=<tag>` to pass against a pushed tag
  without local `replace`.
- PostgreSQL is the durable source of truth for S3 TUS. Redis must not be the
  only copy of protocol session metadata.
