# Anonymous consumer verification

`scripts/anonymous-consumer.sh` verifies public dependency availability without
using developer credentials or existing Go caches. Run it with an installed Go
compiler; the module's Go directive and toolchain remain authoritative.

```sh
scripts/anonymous-consumer.sh source
scripts/anonymous-consumer.sh source /path/to/checkout
scripts/anonymous-consumer.sh published vX.Y.Z
```

The source mode copies only `go.mod` and `go.sum` into a temporary directory,
resolves the complete dependency graph and downloads all modules through
`https://proxy.golang.org`. It rejects `goshared`, `goredis`, `gowebsocket`, and
any `replace` directive. It does not build the source, run the current Go probe,
or establish that the root module itself has been published.

The published mode requires an exact semantic version, including optional
prerelease identifiers. It fetches that version without `replace`, resolves and
downloads its complete graph, then compiles an independent consumer importing
both supported packages: `host` and `hosttest`. It also compiles with the
`integration` tag. No database or external service is contacted by these
compile-only checks. The resulting graph is checked again after compilation.

Each Go invocation runs with an empty inherited environment, temporary `HOME`,
XDG config directory, module/build caches and GOPATH. `GOENV`, workspaces and
Go authentication are disabled. Netrc and Git configuration are bypassed; Git
prompts and all VCS access are disabled. Only the public Go proxy is allowed,
with the public checksum database and no `direct` fallback. User proxy settings,
private module exceptions, tokens and SSH agents are not inherited. The script
uses the installed Go executable; the Go toolchain may download anonymously
through the same public proxy. All temporary files are removed on exit.

Only positional arguments configure this probe. `TMPDIR`, when supplied, chooses
the temporary workspace parent; it does not share dependency caches or credentials.
Exit status `0` indicates the stated mode passed. Invalid arguments return `2`;
dependency rejection, download/build failure, or unavailable publication returns
nonzero. A `BLOCKED` publication result means the requested version cannot be
resolved anonymously; source verification cannot substitute for that evidence.
The script never publishes a tag or changes the checkout's module files.
