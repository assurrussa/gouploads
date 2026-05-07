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

## Rules

- Host projects should import `github.com/assurrussa/gouploads/host`, not
  `domain/files/*`, `shared/*`, `config`, or `di` directly.
- `shared/*` and `di` may keep their physical package names for internal
  compatibility, but stable consumer access must go through the Host Surface.
- A temporary host adapter may be allowed by the import policy while a published
  version catches up, but new deep imports should not spread.
