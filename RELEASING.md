# Releasing gouploads

This checklist is the reusable-boundary gate for `gouploads`.

## Local Readiness

Before tagging, run:

```bash
make publish-readiness VERSION=v0.10.0-alpha.2
```

This verifies `go.mod`/`go.sum` tidiness, the host facade, host test-support
package, integration-tag `hosttest` surface, PostgreSQL TUS fencing test,
external consumer manifest, local
clean consumer probe, integration-tag clean consumer probe, import-policy
checker, and the full module test suite.
The Makefile uses repository-local `.go-cache` paths by default so the check is
stable in sandboxed local environments without polluting scanned Go package
roots.

If the sibling `../site` repository exists, the target also runs the import
policy against `site/backend`, `goadmin`, and `site/fixtures/second-go-host`
from the parent workspace root.
Requested consumer roots are required to exist. A typo in `--repo-root` or a
consumer path must fail the gate instead of silently checking nothing.

## Release Sequence

The published stable baseline is `v0.9.0`. The current source candidate is
`v0.10.0-alpha.2`; it adds the stable `host.NewStorage` facade, CMS-only TUS
route registration, hardened separation between create/resume and generic
delete capabilities, and Yandex Object Storage-compatible multipart
finalization when `ListParts` omits checksum extensions.

1. Run `make publish-readiness VERSION=v0.10.0-alpha.2`.
2. Commit the `gouploads` changes.
3. Tag the reviewed commit, for example `git tag v0.10.0-alpha.2`.
4. Push the commit and tag.
5. Run `make release-readiness VERSION=v0.10.0-alpha.2`.
6. Update host projects to the published version and re-run their platform
   boundary checks.

Do not call the release reusable based only on local `replace` checks. A host
repo can use a local checkout while developing the surface, but the final proof
must resolve the pushed tag without a local path override.

## Supported Surface

Before tagging, confirm that `reference/externalconsumer` lists every supported
external package and no unsupported package:

- runtime embedding: `github.com/assurrussa/gouploads/host`;
- test support: `github.com/assurrussa/gouploads/hosttest`.

Do not tell host projects to import `domain/files/*`, `config`, `di`,
`infrastructure/*`, or `shared/*` directly. If a host needs something from an
internal package, expose the narrow contract through `host` or `hosttest`.

Runtime host code may import only `host`. Test files and packages under
`tests` or `testsupport` may import `hosttest`. The import-policy gate enforces
that split.
The `hosttest` integration helpers are public contracts, not aliases to
`domain/files/tests`.
They are compiled only with `-tags integration`; keep their required
`TEST_PSQL_*` environment variables documented in `docs/host-integration.md`.

## Migration Contract

The `files` and `upload_sessions` migrations are exposed through
`host.MigrationsFS()` and `host.MigrationFiles()`. Keep `host/migrations`
synchronized with `db/migrations`; `go test ./host` checks this. S3 TUS is not
release-ready if a host omits `upload_sessions` or constructs a Redis-only
session store.

The candidate must also prove local and S3/MinIO `NewStorage` construction,
`DeleteBatch`, CMS TUS omission of completion/delete routes, and independent
route guards. These are supported host contracts, not permission shortcuts for
CMS purge policy.

Run `make test-tus-postgres-integration` against PostgreSQL before tagging. The
test proves competing lease exclusion, offset CAS, monotonic fencing revision,
cross-repository visibility, and competing finalize exclusion.

Outbox storage migrations are not owned by `gouploads`; document them as a host
requirement for whichever outbox backend the host uses.

## Published-Version Gate

Local `replace` checks are not enough for release readiness. After tagging and
pushing a candidate version, verify a clean consumer without local path
overrides:

```bash
make externalconsumer-published VERSION=vX.Y.Z
```

Only call the version externally reusable after this clean consumer resolves the
published tag in both normal and `-tags integration` consumer builds.
