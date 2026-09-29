#!/bin/sh
# Verify dependencies and published public packages without developer credentials.
set -eu

usage() {
    printf '%s\n' 'Usage: scripts/anonymous-consumer.sh source [CHECKOUT]' \
        '       scripts/anonymous-consumer.sh published vX.Y.Z[-PRERELEASE]'
    exit 2
}

[ "$#" -ge 1 ] && [ "$#" -le 2 ] || usage
mode=$1
module=github.com/assurrussa/gouploads
script_root=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
go_binary=$(command -v go) || { printf '%s\n' 'Go is required' >&2; exit 2; }
case "$go_binary" in /*) ;; *) printf '%s\n' 'Go must resolve to an absolute executable path' >&2; exit 2;; esac

case "$mode" in
    source)
        source_root=${2:-$script_root}
        [ -f "$source_root/go.mod" ] && [ -f "$source_root/go.sum" ] || usage
        ;;
    published)
        [ "$#" -eq 2 ] || usage
        version=$2
        printf '%s\n' "$version" | grep -Eq '^v(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z-]+(\.[0-9A-Za-z-]+)*)?$' || usage
        # SemVer forbids leading zeros in numeric prerelease identifiers.
        prerelease=${version#*-}
        if [ "$prerelease" != "$version" ]; then
            old_ifs=$IFS
            IFS=.
            set -- $prerelease
            IFS=$old_ifs
            for identifier do
                case "$identifier" in
                    *[!0-9]*) ;;
                    0) ;;
                    0*) usage ;;
                esac
            done
        fi
        ;;
    *) usage ;;
esac

probe_root=$(mktemp -d "${TMPDIR:-/tmp}/gouploads-anonymous.XXXXXX")
cleanup() {
    # Go makes downloaded module directories read-only.
    chmod -R u+w "$probe_root"
    rm -rf -- "$probe_root"
}
trap cleanup EXIT HUP INT TERM
mkdir -p "$probe_root/home" "$probe_root/xdg" "$probe_root/module" "$probe_root/tmp" \
    "$probe_root/gopath" "$probe_root/modcache" "$probe_root/buildcache"

# env -i discards tokens, proxies, SSH agents, Go flags and developer settings.
# No direct fallback or VCS access is permitted, even for transitive modules.
anonymous_go() {
    env -i \
        PATH="$(dirname -- "$go_binary"):/usr/bin:/bin" \
        HOME="$probe_root/home" XDG_CONFIG_HOME="$probe_root/xdg" \
        TMPDIR="$probe_root/tmp" GOENV=off GOWORK=off GOAUTH=off \
        GOPATH="$probe_root/gopath" GOMODCACHE="$probe_root/modcache" \
        GOCACHE="$probe_root/buildcache" GOPROXY=https://proxy.golang.org \
        GOSUMDB=sum.golang.org GOPRIVATE= GONOPROXY=none GONOSUMDB=none \
        GOVCS='*:off' GOTOOLCHAIN=auto CGO_ENABLED=0 \
        NETRC=/dev/null GIT_CONFIG_NOSYSTEM=1 GIT_CONFIG_GLOBAL=/dev/null \
        GIT_TERMINAL_PROMPT=0 \
        "$go_binary" "$@"
}

reject_forbidden() {
    if grep -Eq 'github\.com/assurrussa/(goshared|goredis|gowebsocket)(@|[[:space:]]|/|$)' "$1"; then
        printf '%s\n' 'FAIL: forbidden dependency in anonymous module graph:' >&2
        grep -E 'github\.com/assurrussa/(goshared|goredis|gowebsocket)(@|[[:space:]]|/|$)' "$1" >&2
        exit 1
    fi
}

if [ "$mode" = source ]; then
    cp "$source_root/go.mod" "$source_root/go.sum" "$probe_root/module/"
else
    printf 'module anonymous.example/gouploads-probe\n\ngo 1.27.0\n\nrequire %s %s\n' \
        "$module" "$version" > "$probe_root/module/go.mod"
fi
cd "$probe_root/module"

# Reject replace directives: neither mode can substitute locally available code.
if grep -Eq '^[[:space:]]*replace([[:space:]]|\()' go.mod; then
    printf '%s\n' 'FAIL: replace directives are not permitted' >&2
    exit 1
fi
reject_forbidden go.mod
printf 'Anonymous %s: resolving the public dependency graph\n' "$mode"
if ! anonymous_go mod graph > "$probe_root/graph.txt"; then
    printf '%s\n' 'BLOCKED: dependency graph cannot resolve through the public proxy' >&2
    exit 1
fi
reject_forbidden "$probe_root/graph.txt"
if ! anonymous_go mod download all; then
    printf '%s\n' 'BLOCKED: dependencies cannot download through the public proxy' >&2
    exit 1
fi

if [ "$mode" = published ]; then
    cat > consumer_test.go <<'GO'
package consumer_test

import (
    _ "github.com/assurrussa/gouploads/host"
    _ "github.com/assurrussa/gouploads/hosttest"
)
GO
    printf 'Anonymous published: compiling host and hosttest at %s\n' "$version"
    anonymous_go test -mod=mod -run '^$' ./...
    anonymous_go test -mod=mod -tags integration -run '^$' ./...
    # Compilation may add module requirements; check the complete resulting graph.
    anonymous_go mod graph > "$probe_root/graph.txt"
    reject_forbidden "$probe_root/graph.txt"
    printf 'PASS: anonymous public host/hosttest consumer at %s\n' "$version"
else
    printf '%s\n' 'PASS: anonymous source dependency graph and downloads (no source build)'
fi
