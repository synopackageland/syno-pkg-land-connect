# Syno Connect Center

Syno Connect Center is a native Synology DSM package for managing remote access
to your NAS and its web apps. It brings public links, private networks and
direct IPv6 access into one administrator interface.

Requires DSM 7.2 or later.

Each connection method can be enabled independently. The app runs as its own
package account and uses DSM administrator sign-in; Node.js is needed only for
development, not on the NAS.

## Features

- Public web access through Cloudflare, with free links or your own domain.
- Private networking through Tailscale, Headscale or NetBird.
- Direct IPv6 access with connection diagnostics.
- Approved web app URLs, access controls and mobile browser access with QR codes.
- Connector downloads, updates and rollback using verified official binaries.
- Connection status, troubleshooting and CLI operations.

## Architecture

```text
DSM administrator -> Vue UI / CLI -> SDK-authenticated Go CGI
                                             |
                                      private Unix socket
                                             |
                                  connectd -> connect-worker
                                             |
                              providers -> official connectors
```

The SDK enforces administrator access. The package supervisor keeps `connectd`
running; `connect-worker` owns provider sessions, app routing and connection
checks. Connector programs are downloaded and verified on demand.

## Development

Install Node.js (LTS), Corepack/pnpm, Go, Python and tar. Use the dependency and
toolchain pins in [package.json](package.json),
[frontend/package.json](frontend/package.json), [backend/go.mod](backend/go.mod)
and [scripts/go_toolchain.py](scripts/go_toolchain.py); retain both lockfiles.
Backend tests also require Docker or the prepared Linux fixture described in
the [development guide](docs/development.md).

Run from the repository root:

```sh
corepack enable pnpm
pnpm install --frozen-lockfile --ignore-scripts --registry https://registry.npmjs.org
pnpm --dir frontend install --frozen-lockfile --ignore-scripts --registry https://registry.npmjs.org
```

Start the local UI and synthetic backend:

```sh
pnpm run dev
```

Open `http://127.0.0.1:19036/`. This fixture uses simulated connections without
DSM sign-in or changes to a NAS.

Run tests and static checks:

```sh
pnpm test
pnpm run lint
pnpm run typecheck
pnpm run ctl:check
```

Build the frontend alone, or package and inspect all supported SPK architectures:

```sh
pnpm --dir frontend run build
export SYNOPKGLAND_CATALOG=/path/to/catalog.yaml
pnpm run build
```

The package build checks the manifest, builds native binaries and the UI,
inspects each unsigned SPK and runs artifact tests. Outputs go to `dist/` for
`x86_64`, `aarch64` and `armv7`. `pnpm run gates` combines all checks and packaging.

The store text for released versions is in the public
[synopackageland/catalog](https://github.com/synopackageland/catalog): clone it
and point `SYNOPKGLAND_CATALOG` at its `catalog.yaml`.

See [development details](docs/development.md) for catalog requirements, focused
tests and mobile assets; and [CLI operations](docs/ctl.md) for automation. Provider limits are in
[NetBird](docs/netbird.md), [Tailscale](docs/tailscale.md),
[Cloudflare Access](docs/cloudflare-access.md) and
[private proxy](docs/private-proxy-provider.md).

## License

[MIT](LICENSE). See [NOTICE](NOTICE) and [LICENSES](LICENSES) for artwork and
third-party attribution.
