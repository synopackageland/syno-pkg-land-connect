#!/usr/bin/env bash
# Run production IPC/runtime fixtures without relying on the host's UID or /tmp.
# The only root action initializes a shared compiler cache, never package code.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
bindings='-X github.com/synopackageland/syno-pkg-land-sdk/ipc.packageID=SynoPkgConnect -X github.com/synopackageland/syno-pkg-land-sdk/ipc.executorTier=none'
# A disposable package-account gate container can run directly without Docker
# access. Otherwise create that environment here instead of changing the host.
if test "$(uname -s)" = Linux && test "$(id -u)" != 0 &&
   test "$(stat -f -c %T /tmp)" = tmpfs &&
   test "$(stat -c '%a:%u' /var/packages/SynoPkgConnect/var/run 2>/dev/null)" = "700:$(id -u)"; then
  cd "$root/backend"
  exec go test -p 1 -ldflags="$bindings" "$@"
fi
(cd "$root/backend" && go mod download)
modules=$(go env GOMODCACHE)
image=public.ecr.aws/docker/library/golang:1.26.10-bookworm@sha256:23ce71337b726608afb1bc00986e630a05034a87a87ea1fc2cd1ae5cf33387eb
cache=synopkgland-cache-connect-go
docker run --rm --network none --user 0:0 -v "$cache:/cache" "$image" chown 1000:1000 /cache
docker run --rm --network none --user 1000:1000 \
  --tmpfs /tmp:rw,exec,mode=1777 \
  --tmpfs /var/packages/SynoPkgConnect/var:rw,uid=1000,gid=1000,mode=0700 \
  -v "$root:/workspace:ro" -v "$modules:/gomod:ro" -v "$cache:/cache" \
  -w /workspace/backend -e HOME=/tmp -e GOCACHE=/cache -e CONNECT_TEST_BINDINGS="$bindings" \
  -e GOMODCACHE=/gomod -e GOPROXY=off -e GOSUMDB=off \
  "$image" sh -eu -c '
    mkdir -m 0700 /var/packages/SynoPkgConnect/var/run
    # Packages share the fixed production control.sock; isolate their lifetimes.
    # Bind the same SDK identity as scripts/prepare.py and the CLI packer.
    exec go test -p 1 -ldflags="$CONNECT_TEST_BINDINGS" "$@"
  ' sh "$@"
