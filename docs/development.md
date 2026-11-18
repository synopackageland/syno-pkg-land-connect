# Development details

Use the installation, local development and build commands in the
[README](../README.md). Keep the root and frontend pnpm lockfiles unchanged
unless deliberately updating dependencies.

## Build and catalog inputs

`scripts/verify_cli.py` checks the installed CLI against the root lockfile.
`scripts/go_toolchain.py` selects the Go toolchain used for every packaged binary.
The manifest and locked dependencies are sufficient to rebuild the package;
no local SDK source checkout is required.

Set `SYNOPKGLAND_CATALOG` in the environment or a local `.env` file before
checking, building or inspecting packages. It must point to a catalog accepted
by the installed CLI, with package descriptions and release notes for every
supported language. Each manifest version/build needs its own entry under
`metadata.changelog.releases`. The CLI also accepts `--catalog <catalog.yaml>`
when invoked directly. Inspection must not report `CATALOG_TEXT_DRIFT`.

`pnpm run build` invokes `scripts/build.py`, which checks the manifest, builds
through the installed CLI, adds the uninstall reminder, inspects every SPK and
runs `tests/artifact.test.mjs`. The manifest's UI hook runs `scripts/prepare.py`
to audit and build the native daemon payloads, compile the frontend, install the
checked mobile web bundle and collect third-party notices.

The output pattern is `dist/SynoPkgConnect-<version>-<build>-<arch>.spk`.
Connector clients are obtained separately on the NAS; see
[binary management](binary-management.md) and
[supervisor memory](supervisor-memory.md).

## Local fixtures and tests

`pnpm run dev` builds `backend/cmd/dev`, starts its loopback API and runs Vite.
Synthetic sessions support connection and app UI work without signing in to
DSM, installing connector clients or changing network configuration. Fixture
addresses do not establish external connectivity.

`pnpm test` runs Python build tests, Node contract tests, backend tests and
frontend tests. Its backend stage uses `scripts/backend-test.sh`, which downloads
locked Go modules before starting an offline, nonroot container with read-only
source/module mounts and private tmpfs runtime directories. It binds the same
SDK package identity and executor tier as production, and tests packages
serially because their fixtures share a fixed control socket.

For a focused backend run:

```sh
bash scripts/backend-test.sh ./internal/binaries ./internal/app -count=1
```

A prepared nonroot Linux fixture can run without a Docker socket when `/tmp`
is tmpfs and `/var/packages/SynoPkgConnect/var/run` belongs to its current UID
with mode `0700`. Keep the product's UID, filesystem and socket checks enabled.
The shared Docker volume retains compilation caches; runtime tmpfs disappears
when the container exits.

Frontend checks run locally by default. Set `SYNOPKGLAND_DOCKER_SSH` to a chosen
build host to run frontend tests, lint, typechecking and the packaging UI hook in
that host's digest-pinned Node container. The helper installs from the frozen
lockfile, uses the shared npm cache and waits for a gate slot. Set
`SYNOPKGLAND_DOCKER_NPM_REGISTRY` if that host requires another registry.

The custom-app sign-in gate also has a local browser regression:

```sh
pnpm exec playwright install chromium
(cd backend && CONNECT_GATE_BROWSER=1 TMPDIR=/tmp go test ./internal/apps -run TestCustomGatePublicBrowser -count=1 -v)
```

It uses loopback HTTPS and the bundled Worker's header handling to test form
origins, cross-site refusal, cookies and redirects without contacting DSM or
Cloudflare. Run device tests only against an explicitly authorized target.

## Mobile web assets

The phone browser page is a relative-path export from the mobile project,
vendored unchanged in `vendor/mobile-web`. Its `build.json` records the source
commit and file hashes. To update it from a local mobile checkout or verify it:

```sh
python3 scripts/mobile_web.py update /path/to/mobile-repo
python3 scripts/mobile_web.py check
```

The update command also refreshes the Material Icons secret-scan exception and
generates `vendor/mobile-web-THIRD_PARTY.md` from the same export's source maps
and font metadata. The check rejects notices from a different source commit.
Packaging copies these notices into `LICENSES/`.

The manifest's `ui.stable_pages` entry preserves the phone URL across hashed UI
builds. The installed PWA manifest uses that stable URL and package scope;
vendored files retain their original hashes. `ui.short_paths` adds the short
address `/m`, which DSM's nginx redirects to the stable page while Connect
runs; the Overview's link and QR codes use it. It needs DSM 7.2 or later, so
the package requires DSM 7.2-64570.

To refresh the phone preview assets, use `scripts/phone-apps.mjs` on an authorized
DSM fixture with its language set to English, then `scripts/phone-preview.mjs`.
The first script reads access parameters from the environment and substitutes
neutral device/folder names. Inspect every captured image before committing it.
The preview script needs `cwebp` and writes WebP assets and button positions to
`frontend/src/assets/phone/`. Both capture scripts require Playwright Chromium.

Validate the resulting cards and QR destinations locally. The decoder also
requires OpenCV in the selected Python environment:

```sh
node tests/phone-card-screens.mjs dist/phone-card-screens
python3 -I tests/phone-qr-decode.py dist/phone-card-screens
```

## Runtime and SDK boundaries

The Go SDK owns DSM session verification, administrator admission, CGI serving
and bounded JSON parsing. App permissions live in
`backend/internal/api/permissions.json`. Browser mutations must carry same-origin
request evidence; scripts exercising those routes must supply it explicitly.
DSM external-access changes use only the requesting administrator's live session.

The Vue frontend uses the SDK's `AppShell` and context with the shared UI library.
The package manifest declares no executor, delegate or portal capability.
Uninstall does not automatically remove DSM Application Privileges rules; the
administrator removes retained rules in DSM when needed.

See [CLI operations](ctl.md), [app routing](../backend/internal/apps/README.md)
and [access composition](../backend/internal/appaccess/README.md) for API and
runtime contracts. Artwork provenance is recorded in
[assets/icon-source.json](../assets/icon-source.json) and [NOTICE](../NOTICE).

## Internal-name check

`scripts/check-internal-names.sh` (run by `npm test` and fast CI) fails when a
file names an internal `.syno` host, a company-internal or personal domain, a
private IPv4 address other than the common examples, a work-thread id, a
`/Users/<name>` folder or a secret. List machine names outside the repository in
`SYNOPKGLAND_INTERNAL_NAMES` or a file named by `SYNOPKGLAND_INTERNAL_NAMES_FILE`
to check them too. A commit hook can run the same rules on just the added lines
and the message: `--staged` and `--message FILE`.
