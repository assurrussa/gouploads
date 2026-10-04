# Releasing gouploads

This checklist is the reusable-boundary gate for `gouploads`.

## Local Readiness

For an untagged candidate, run `make source-readiness` with the integration
services available. This read-only source gate includes original-only E2E and
an anonymous dependency probe. Set `VERSION` to the new exact semver tag before tagging, then run:

```bash
make publish-readiness VERSION="$VERSION"
```

This verifies `go.mod`/`go.sum` tidiness, the host facade, host test-support
package, integration-tag `hosttest` surface, PostgreSQL TUS fencing test,
direct-source S3 integration, mandatory local-filesystem and MinIO/PostgreSQL
portable-media E2E,
external consumer manifest, local
clean consumer probe, integration-tag clean consumer probe, import-policy
checker, and the full module test suite.
The Makefile uses shared Go caches outside the checkout, rooted at
`$(HOME)/dev/projects/.cache/go`. `GO_SHARED_CACHE_ROOT` and individual cache
variables remain overridable; sandboxed runs require permitted cache access.

`make publish-readiness` intentionally runs mutating `make prepare` before the
read-only checks and rejects any resulting diff. For normal development, use
package-scoped tests and one final `make check`; five-run race stress and HTML
coverage remain explicit `make test-race` and `make cover-html` diagnostics.

PostgreSQL integration targets honor explicit `TEST_PSQL_ADDRESS_LOCAL` and
`TEST_PSQL_PORT_LOCAL` values first. Without overrides, host-side runs discover
the published port of the live Compose service named
`integration-postgres-tests`; native non-Compose runs fall back to
`127.0.0.1:5432`. Container-side runs retain the internal
`integration-postgres-tests:5432` default. The gate verifies dependencies but
does not provision them.

If the sibling `../site` repository exists, the target also runs the import
policy against `site/backend`, `goadmin`, and `site/fixtures/second-go-host`
from the parent workspace root.
Requested consumer roots are required to exist. A typo in `--repo-root` or a
consumer path must fail the gate instead of silently checking nothing.

## Release Sequence

Choose a new immutable tag for the reviewed commit. Examples below illustrate
the sequence, not an instruction to reuse an existing tag. The source gate
never substitutes an older published tag for the candidate's evidence.

1. Run `make publish-readiness VERSION="$VERSION"`.
2. Commit the `gouploads` changes.
3. Tag the reviewed commit, for example `git tag "$VERSION"`.
4. Push the commit and tag.
5. Run `make release-readiness VERSION="$VERSION"`.
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

## Anonymous availability

`anonymous-source` downloads the source dependency graph with isolated caches
and no host credentials. `anonymous-published VERSION=<tag>` additionally
resolves and compiles the actual tag with no local replacement. The latter is
required by `release-readiness`; a private root repository blocks public
reusability even when all dependencies are public. See
[anonymous consumer checks](docs/anonymous-consumer.md).

## Audio release acceptance

The additive `host.FileTypeAudio` capability requires a new minor release.
Keep default policies unchanged. Run audio policy/ingress/TUS regressions and
`TestIntegrationAudioOriginals` on both local and S3 storage, with a real
PostgreSQL outbox. These generated MP3/WAV fixtures must retain their bytes,
canonical MIME, size and checksum after finalization and replay. Verify that
media-resizer mode rejects audio without staging, metadata or resize jobs.
After publication, compile `host.FileTypeAudio` from the exact published tag
without a local replacement before updating consumer pins.
