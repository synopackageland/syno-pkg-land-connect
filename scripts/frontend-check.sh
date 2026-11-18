#!/usr/bin/env bash
# Usage: scripts/frontend-check.sh <command>
# Runs command with sh in frontend/: here, or with SYNOPKGLAND_DOCKER_SSH=<ssh
# host> on Linux in Docker on that host, so this machine's load cannot slow
# its tests. There it runs in the official Node.js image, in a gate slot
# (scripts/docker-engine.sh), in the working tree with its untracked files
# (scripts/docker-tree.sh), after pnpm install --frozen-lockfile; the package cache is the host's shared
# synopkgland-cache-npm volume. SYNOPKGLAND_DOCKER_NPM_REGISTRY: the registry
# pnpm uses there, for a network that blocks registry.npmjs.org; the lockfile's
# integrity still holds. The host's CA bundle is trusted there, for a network
# that inspects TLS. frontend/test/e2e.test.ts's Go fixture (backend/cmd/dev)
# is built here for linux/amd64, as the SDK module is private, and goes in as
# CONNECT_DEV_FIXTURE. vitest takes half the host's CPUs
# (VITEST_MAX_WORKERS): the host's other gate slot has the rest. The container
# is <SYNOPKGLAND_CONTAINER_PREFIX (default syno-pkg-connect)>-frontend.
set -euo pipefail
root=$(cd "$(dirname "$0")/.." && pwd)
command=$1
if test -z "${SYNOPKGLAND_DOCKER_SSH:-}"; then
	cd "$root/frontend"
	exec sh -c "$command"
fi

. "$root/scripts/docker-engine.sh"
registry=${SYNOPKGLAND_DOCKER_NPM_REGISTRY:-}
# Build hooks can export only dist/ on stdout; tool output stays on stderr.
export_dist=${SYNOPKGLAND_FRONTEND_EXPORT:-0}
case $registry in *[!A-Za-z0-9:/._-]*) echo "invalid SYNOPKGLAND_DOCKER_NPM_REGISTRY: $registry" >&2; exit 2 ;; esac
work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT
cd "$root"
git ls-files -z --others --exclude-standard | COPYFILE_DISABLE=1 tar --no-xattrs -c --null -T - -f "$work/untracked.tar"
(cd backend && CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -o "$work/.gate/fixture" ./cmd/dev)
COPYFILE_DISABLE=1 tar --no-xattrs -c -f "$work/fixture.tar" -C "$work" .gate
cat "$work/untracked.tar" "$work/fixture.tar" >"$work/extra.tar"
# Corepack uses a shared cache and the selected registry; its launcher goes in
# /tmp/bin because this container runs as uid 1000, not root.
# The CA bundle is mounted only when the host has one.
ca=/etc/ssl/certs/ca-certificates.crt
image=public.ecr.aws/docker/library/node:24.20.0-bookworm@sha256:be23f54a88d34e8824c741b19b91064094f92c1c97b194144bfc8b50d67258e2
cache_volumes "$image" synopkgland-cache-npm
SYNOPKGLAND_DOCKER_SLOT=1 SYNOPKGLAND_DOCKER_TAR="$work/extra.tar" \
SYNOPKGLAND_DOCKER_ARGS="-e CI=1 -e VITEST_MAX_WORKERS=\$((\$(nproc) / 2)) -e CONNECT_DEV_FIXTURE=/tmp/src/.gate/fixture -v synopkgland-cache-npm:/cache/npm -e npm_config_cache=/cache/npm -e npm_config_store_dir=/cache/npm/pnpm-store \
${registry:+-e npm_config_registry=$registry -e COREPACK_NPM_REGISTRY=$registry} -e COREPACK_HOME=/cache/npm/corepack \$(test -f $ca && echo -v $ca:/tmp/host-ca.crt:ro -e NODE_EXTRA_CA_CERTS=/tmp/host-ca.crt)" \
"$root/scripts/docker-tree.sh" "${SYNOPKGLAND_CONTAINER_PREFIX:-syno-pkg-connect}-frontend" "$image" \
 "mkdir -p /tmp/bin && export PATH=/tmp/bin:\$PATH && cd frontend && corepack enable --install-directory /tmp/bin pnpm && pnpm install --frozen-lockfile --reporter=silent $(test "$export_dist" = 1 && echo '>&2') && $command $(test "$export_dist" = 1 && echo '>&2 && tar -cf - dist')"
