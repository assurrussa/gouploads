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

Read in this order:

1. `AGENTS.md`
2. `README.md`
3. `CONTEXT.md`
4. `docs/host-integration.md`
5. `docs/project-map.md`
6. `RELEASING.md`
7. `reference/externalconsumer`
8. Relevant code, tests, migrations, configs, and generated contracts

Use `$project-context-router` when a task needs cross-project context or the
shared wiki. Expose the shared wiki root through `AGENT_CONTEXT_ROOT` or let `$project-context-router` resolve it for the current session.
After local grounding, read:

Do not hard-code machine-local absolute paths in this public repository.

- `streams/wiki/index.md`
- `streams/wiki/glossary.md`
- `streams/wiki/platforms/gouploads.md`
- `streams/wiki/platforms/media-resizer.md`
- `streams/wiki/platforms/outbox.md`

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
- `make check`: full local check, including generation, formatting, lint, race
  tests, and coverage HTML.
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

When the user asks about a library, framework, SDK, API, CLI tool, or cloud
service, use the `ctx7` CLI first:

```bash
npx ctx7@latest library <name> "<user question>"
npx ctx7@latest docs <libraryId> "<user question>"
```

Do not use Context7 for ordinary refactors, business-logic debugging, code
review, or general programming concepts.
