# Repository Guidelines

## Project Identity

`gouploads` is the Go upload/storage orchestration module at
`github.com/assurrussa/gouploads`. It provides file upload contracts, TUS
resumable upload support, local and S3/Ceph storage wiring, media-resizer
callbacks, cleanup use cases, and upload-related outbox job registration for
host Go/Fiber applications.

This repository is a reusable library/module, not only a copied host service.
When changing behavior, preserve the public host-facing boundary unless the task
explicitly asks to expand it.

## Source Order

Local verified docs and code are the source of truth for commands, public APIs,
config keys, supported imports, runtime behavior, and release gates.

Read this `AGENTS.md` and the relevant part of `README.md` first, then
select the source for the task:

- Package/domain boundaries: `CONTEXT.md` and `docs/project-map.md`.
- Host wiring or API integration: `docs/host-integration.md`.
- Published compatibility or release work: `RELEASING.md` and
  `reference/externalconsumer`.
- Behavior changes: the affected code, tests, migrations, configs and generated
  contracts. Do not read every document for a local fix.

Use `$project-context-router` when a task needs cross-project context or the
shared wiki. Expose the shared wiki root through `AGENT_CONTEXT_ROOT` or let `$project-context-router` resolve it for the current session.
Do not hard-code machine-local absolute paths in this public repository.

When shared context is needed, follow `streams/AGENTS.md` and its query route.
Reuse already loaded root rules, PII policy and glossary. Open the known hub
and only the topic relevant to the task:

- `streams/wiki/platforms/gouploads.md`

For integration work, open only the affected neighbour hub:

- `streams/wiki/platforms/media-resizer.md`
- `streams/wiki/platforms/outbox.md`

Use `streams/wiki/index.md` only to locate an unknown area or answer an overview
question. This is a task router, not a mandatory list of wiki pages.

If local verified docs/code conflict with the shared wiki, treat the wiki as
stale. When the task includes documentation upkeep, update the relevant platform
page after verification. Keep wiki pages concise and contract-focused; do not
copy whole README files into shared context.

## Public Contract

Supported external package paths are defined by
`reference/externalconsumer`.

- Runtime embedding package: `github.com/assurrussa/gouploads/host`
- External test-support package: `github.com/assurrussa/gouploads/hosttest`

Host runtime code should not import `domain/files/*`, `config`, `di`,
`infrastructure/*`, `shared/*`, or other deep packages directly. Test files and
packages under `tests` or `testsupport` may use `hosttest`.

When a host needs a new stable capability, expose the narrow contract through
`host` or `hosttest` and update `reference/externalconsumer`, docs, release
notes, and boundary checks together.

## Important Docs

- `README.md`: overview, install, quick start, and stable surface summary.
- `CONTEXT.md`: repo glossary and reusable-boundary rules.
- `docs/host-integration.md`: detailed host integration contract.
- `docs/project-map.md`: concise package map, runtime flow, config keys, and
  verification gates for future agents.
- `RELEASING.md`: reusable-boundary release checklist and published-version
  gate.
- `implementation-notes.md`: append non-obvious decisions, tradeoffs, and
  verification outcomes made while implementing specs.

## Commands

Use the Makefile targets because they set repository-local Go cache paths.

- `make release-readiness`: main reusable-boundary gate.
- `make prepare`: mutating tidy, generation, formatting, and lint fixes.
- `make check`: source-read-only local verification with formatting, vet, lint,
  and one race+coverage test pass.
- `make full`: preparation followed by verification.
- `make test-race`: explicit five-run race stress diagnostic.
- `make cover-html`: explicit HTML coverage artifact.
- `make test-surface`: focused public surface and probe tests.
- `make test-surface-integration`: `hosttest` integration-tag surface check.
- `make externalconsumer-local`: clean consumer probe with local `replace`.
- `make externalconsumer-published VERSION=vX.Y.Z`: clean consumer probe against
  a pushed published tag.
- `go run ./cmd/importpolicy --repo-root .. --consumers site/backend,goadmin,site/fixtures/second-go-host`:
  strict host import boundary check when the sibling `../site` repo exists.

Direct `go` commands may need local cache env vars in sandboxed environments:

```bash
GOCACHE="$PWD/.go-cache/gocache" GOPATH="$PWD/.go-cache/gopath" go test ./...
```

The first direct `go` command can fail if it uses the user-level Go build cache;
prefer Makefile targets or the local cache env vars above.

During implementation, prefer tests for the affected package or public
surface. Run `make check` once after a coherent batch; do not stack it with
`make test`, `make test-race`, and `make cover-html` on an unchanged tree.

## Code Generation

Generated files are committed and marked with `DO NOT EDIT`.

- `options-gen` produces `*_options.gen.go` files.
- `toolsmocks` wraps `mockgen` and produces files under `mocks/`.
- Run `make generate` or `go generate ./...` after changing structs or
  interfaces that own generation directives.

Do not hand-edit generated files unless the task is specifically to repair the
generator output and the generator cannot be run.

## Testing Expectations

For documentation-only changes, at minimum run `git diff --check`.

For public API, supported imports, config, migrations, outbox jobs, host facade,
or hosttest changes, run `make release-readiness`. If the change is intended for
a real reusable release, also run the published-version gate after the new tag is
pushed.

Local `replace` or `make externalconsumer-local` is useful during development,
but it is not enough to claim published reusable readiness.

## Documentation Lookup

Use `$find-docs` for version-sensitive library, framework, SDK, API and CLI
questions. It selects an available documentation tool, resolves the version and
owns query limits and fallback. Reuse applicable docs already fetched in this
task. Ordinary refactors, scripts, business logic and reviews need no lookup
unless an external API contract is the unresolved question.
