#!/bin/sh
set -eu

: "${CONNECT_FIXTURE_PREFIX:?Set a unique prefix for this work's disposable container}"
case "$CONNECT_FIXTURE_PREFIX" in *[!a-zA-Z0-9_-]*|'') exit 2;; esac
repo=$(CDPATH= cd -- "$(dirname -- "$0")/.." && pwd)
modules=$(go env GOMODCACHE)
image=${CONNECT_FIXTURE_IMAGE:-public.ecr.aws/docker/library/golang:1.26.10-bookworm}

# All privileges below belong to the disposable container's initialization.
# Both production server and consumer tests run under distinct nonroot UIDs.
# No published ports, host network, provider credentials or external network.
docker run --rm --network none --name "$CONNECT_FIXTURE_PREFIX-ingress" \
  -v "$repo:/workspace:ro" -v "$modules:/gomod:ro" -w /workspace/backend \
  -e GOMODCACHE=/gomod -e GOPROXY=off -e GOSUMDB=off "$image" sh -eu -c '
  useradd -M -o -u 1000 SynoPkgConnect
  useradd -M -o -u 1001 syngle
  mkdir -p /fixture /var/packages/syngle/conf
  chown 1000:1000 /fixture
  chmod 711 /fixture
  printf "%s\n" "{\"defaults\":{\"run-as\":\"package\"}}" > /var/packages/syngle/conf/privilege
  go test -c -o /tmp/ingress.test ./internal/ingress
  CONNECT_INGRESS_PEER_FIXTURE=server setpriv --reuid=1000 --regid=1000 --clear-groups /tmp/ingress.test -test.run "^TestIngressPeerFixture$" &
  server=$!
  trap "kill $server 2>/dev/null || true" EXIT
  count=0
  while ! test -f /fixture/ready; do
    count=$((count + 1)); test "$count" -lt 100 || exit 1
    kill -0 "$server"; sleep 0.05
  done
  CONNECT_INGRESS_PEER_FIXTURE=consumer setpriv --reuid=1001 --regid=1001 --clear-groups /tmp/ingress.test -test.run "^TestIngressPeerFixture$" -test.v
  CONNECT_INGRESS_PEER_FIXTURE=foreign setpriv --reuid=1002 --regid=1002 --clear-groups /tmp/ingress.test -test.run "^TestIngressPeerFixture$" -test.v
  CONNECT_INGRESS_PEER_FIXTURE=root /tmp/ingress.test -test.run "^TestIngressPeerFixture$" -test.v
  touch /fixture/done
  wait "$server"
  trap - EXIT
  '
