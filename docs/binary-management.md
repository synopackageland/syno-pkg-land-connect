# Connector binary management (C-BIN)

## Scope and integration contract

`backend/internal/binaries` installs **verified candidates** into the package-user
writable `var/binaries` store. It never executes a download or signals a process.
`Source.Latest` and `Source.Download` are startup-only injectable boundaries;
Download must authenticate all bytes before succeeding. `NewOfficialSource` is
used by connectd. Browser parameters accept `cloudflared`, `tailscale`, `netbird`
and `netbird-server`.

`Manager.Check`, `Update`, `Rollback`, `Status`, `Paths` and `Activate` form the
provider contract. `Activate(ctx, product, hook)` holds the cross-process lock
while the provider decides how to stop, start, check readiness and restore the
running service on failure. The hook receives executable paths, not shell text.
`Paths` rechecks
installed file hashes and resolves a complete candidate set; retain
those exact paths for a launch and do not re-resolve the pair independently.

Current = downloaded candidate, **not running process version**. Each card's
"In use x" tag shows the provider-selected version (falling back to the downloaded
one); only when the downloaded version differs does the card add one line saying it
takes effect after reconnecting. A card offers "Download" when nothing is downloaded,
"Update to x" when a newer version is known and neither when it is current; rollback
sits in its ⋯ menu. The NetBird server card is titled "NetBird server" and shows the same latest-version row; its note says a download takes effect when the server restarts. This is not a readiness
claim; the connection status remains authoritative for process health. Tailscale candidate rollback uses the same serialized restart/recovery hook. C-PROV must not use package-writable binaries
as a privileged/root payload without a separately approved trust design.

Every provider, Cloudflare included, ships without a bundled binary and
requests `Update` before first activation. Missing candidates are an explicit
state; a failed first download leaves the connection disabled.

## Storage and operations

A nonblocking `flock` on `operation.lock` permits one check/update/rollback/
activation across CGI processes and both products. Reads remain available.
`Service` runs update/check/rollback jobs in connectd with a five-minute context;
metadata has a 30-second subdeadline. CGI sends a short request over the existing
0600 Unix control socket and immediately receives status. UI polls until the
daemon's global `busy` clears, including when reopening during a job. Request
cancellation does not cancel accepted work. Daemon shutdown does cancel work and
waits up to five seconds. This is not a durable job queue across daemon restarts:
interrupted progress is identified and a fresh explicit retry is required. No
CGI-owned goroutine is expected to survive its request or process.

### Provider contract integration (e4ceca1)

The provider contract itself is unchanged. `RuntimeAdapter` implements
`provider.BinaryManager.Resolve/Version`, verifies installed executable digests,
and durably pins each product generation so separate `tailscale` and `tailscaled`
resolutions cannot silently cross an update. `SelectedVersion` only observes
already selected inventory; opening settings never selects a new candidate.
`Refresh` is explicit: the provider must call it while stopped and under its
selecting lock. The
selected manifest survives daemon restarts independently of the newest download.
`RestorePreviousSelection` lets the provider restore its prior selection after
stopping a failed replacement child, then restart that known version. A failed
first activation has no previous selection; the provider must stay disabled.

connectd constructs this adapter from the binary store alone.
`RuntimeInstalledHook` maps cloudflared, tailscale and netbird to their
providers. It calls the
concrete selector's `UpdateBinaries` method under the same lock as connect,
disconnect and restore; the shared Runtime/BinaryManager interfaces are unchanged.
The adapter's `PrepareRefresh` verifies the candidate and captures the exact old
selection before any stop. Active updates persist a disabled gate, stop the old
child, switch the selection, restore the provider, and check readiness before
restoring enabled intent. Failure stops the replacement, restores the exact old
selection, and restarts the old version; failed cleanup leaves the gate disabled.
An inactive provider only selects the verified binaries and is not started.
`WithServer` routes `netbird-server` to the self-hosted server instead
([netbird-server.md](netbird-server.md)): it applies the selection, restarts a
running server and waits for its health check, and reverts and restarts the
previous version on failure. A server that is off only selects the binary.
The server image publishes linux/amd64, linux/arm64 and linux/arm/v7.
Same-selection updates are a no-op.

connectd injects the same RuntimeAdapter into TailscaleExec and NetBirdExec.
Optional runtime Preflight runs before Selection disables the old connection.
Unavailable/unverified Tailscale binaries return a stable `binary_required` code
through the Unix transport and CGI, with a nine-language instruction to download
first. No process, login or provider switch occurs on that refusal.

Package shutdown cancels binary jobs before acquiring the selecting lock. If an
update is cancelled, it restores old binary selection and accepted restart intent
but does not spawn another child during shutdown. The job service remains bounded
and a failure is surfaced rather than reported as successful activation.

Downloads stream to a private staging directory (256 MiB compressed cap).
Tailscale requires both executables; unpacking limits total regular-file sizes
to 512 MiB and rejects absolute/noncanonical/traversing paths, links, special
files, duplicate executables, and excessive entry counts. Non-executable
auxiliary regular files are skipped. Executables are mode 0700. Files and staging
directories are synced before an atomic JSON manifest rename publishes the pair.
The manifest preserves a previous candidate for rollback and a high-water version
that rollback does not lower. A failed download/verification/extraction preserves
the prior pointers. A killed attempt may leave an unreferenced staging directory. Retired complete
versions are also retained because a provider can still have one pinned. Automatic
reclamation is intentionally deferred to a provider-aware lifecycle; the manifest
exposes only current and previous, not an unbounded rollback list.

Versions are numeric stable triples, compared component-wise; prerelease,
leading-zero, overflow and path-like versions are refused. Initial installation
cannot independently prove freshness of HTTPS metadata, and this does not claim
TUF-style signed metadata expiration. An explicit local rollback is the only
supported downgrade. No arbitrary version or download URL is accepted by API.

## Official sources and verification

Checked 2026-10-04. These are implementation-source findings, not DSM runtime
compatibility claims.

| Product | Discovery / files | Verification | Confidence |
| --- | --- | --- | --- |
| cloudflared | [GitHub latest release API](https://api.github.com/repos/cloudflare/cloudflared/releases/latest), exact `cloudflared-linux-<arch>` asset | SHA-256 from GitHub asset digest, with exact release-body checksum fallback; no unsigned fallback | high |
| Tailscale | [stable JSON](https://pkgs.tailscale.com/stable/?mode=json), exact `tailscale_<version>_<arch>.tgz` | Mandatory distsign, not checksum-only fallback | high |
| NetBird server | `ghcr.io/netbirdio/netbird-server`, tag = latest stable NetBird release; top layer `go/bin/netbird-server` | Anonymous pull token; index → platform manifest → layer, each checked against its SHA-256 digest | high |

cloudflared's [release publishing code](https://github.com/cloudflare/cloudflared/blob/master/github_message.py)
adds checksums to release text. A standalone Linux binary signature was not
established in the inspected release flow (confidence: moderate); this branch
therefore reports `sha256`, never claims signature verification. Package-manager
GPG signatures must not be confused with signatures for these bare executables.

Tailscale's [distsign protocol](https://github.com/tailscale/tailscale/blob/v1.102.4/clientupdate/distsign/distsign.go)
uses pinned Ed25519 root keys to validate the signing-key bundle, then verifies
the artifact signature over BLAKE2s-256 plus little-endian uint64 byte length.
Keys are fetched at **`https://pkgs.tailscale.com/distsign.pub`** and `.sig`, not
under `/stable/`. Artifact signatures append `.sig` to the tarball URL. Both
[upstream root keys](https://github.com/tailscale/tailscale/tree/v1.102.4/clientupdate/distsign/roots)
are pinned from v1.102.4; rotating the root trust requires a package update.
Upstream BSD notice is in `LICENSES/binaries`. Standard Go Ed25519 verification
is used; no network-updatable trust root or checksum fallback exists.

Manual read-only network probe from this worktree found cloudflared 2026.9.3 and
Tailscale 1.102.4, downloaded the native arm64 Tailscale archive, and successfully
verified its full distsign chain. It did not execute either binary. Versions are
observations, not hardcoded update targets. Automated tests use local TLS servers
and generated keys only; no public-service availability is required.

HTTPS is mandatory, including every redirect. Hosts are exactly api.github.com,
github.com, release-assets.githubusercontent.com and pkgs.tailscale.com; custom
ports, userinfo, other schemes and hosts are refused. Five redirects maximum.
Metadata and signing keys have separate limits. No credentials are attached.

| Go / SPK architecture | cloudflared asset | Tailscale asset | NetBird asset | Evidence |
| --- | --- | --- | --- | --- |
| amd64 / x86_64 | linux-amd64 | amd64 | linux_amd64 | build/vet; official binaries ran on test NAS kernel 4.4 |
| arm64 / aarch64 | linux-arm64 | arm64 | linux_arm64 | build/vet; live signature probe; no NAS execution |
| arm / armv7 | linux-arm | arm | linux_armv6 | official binaries verified on a legacy armv7 test NAS, kernel 3.10, DSM 7.1.1; SPK not installed yet |

Runtime GOARCH selects the asset, not a guessed DSM model string. NetBird
publishes no armv7 build; its official armv6 build is used unmodified. After
the official checksum or signature passes, each extracted executable must also
be a Linux ELF file for the NAS CPU (machine and 32/64-bit), or the update
fails and the current version stays. Other ARMv7 platforms (armada38x, monaco)
need their own acceptance before claiming support.

## API and UI

All four methods are `api.Admin`: `binaries.status`, `.check`, `.update`,
`.rollback`. Input is exactly `{ "product": "cloudflared" | "tailscale" | "netbird" | "netbird-server" }`.
Each product has at most one job. A request for another product while one runs
is queued (phase `queued`, busy) and runs after it, one download at a time;
`binary_busy` means that product already has a queued or running job.
`binary_failed` directs the client to persisted status. Current/latest/previous,
last successful/failed check time, update availability, verification,
provider-selected version, the product's own busy flag, phase and safe errors are
returned; one product's job or failure never shows on another. Submission success means the daemon accepted
the job; the UI waits for busy=false and inspects phase/error for completion. Executable
paths, artifact URLs and high-water internals are not in the response.

`BinarySettings.vue` uses existing UI components inside AppRoot. Its labels cover
en, zh-TW, zh-CN, ja, ko, de, fr, es and it; unknown locales fall back to English.
Opening the panel reads local status only. Downloading does not expose DSM or
create any external resource. Public-exposure consent belongs to C-PROV/C-PORT.

## Validation and remaining acceptance

TDD red runs preceded manager/API/UI implementation and the interrupted-operation
and official-signing-key-path corrections. Fixture tests cover checksum and
root/artifact signature failures, oversize data, hostile archives, interrupted
HTTP, redirect rejection, metadata paths, stable-version comparison and downgrade
prevention, cross-manager exclusivity, cancellation, rollback and activation
refusal. Admin/session route policy tests and UI double-click protection pass.

Required gates: `npm test`, `npm run lint`, `npm run typecheck`, frontend build,
`go test -race ./...`, `go vet ./...`, Linux amd64/arm64 vet, `npm run build` with
both untouched SPK artifact inspections. NAS end-to-end acceptance, real Tailscale activation/readiness/restore,
and ARM runtime compatibility remain unverified. Job request independence,
shutdown cancellation, Unix transport, selected-generation pinning, tamper
refusal and UI polling are covered by local fixtures. Existing npm audit findings concern Vitest and @vitest/mocker;
dependency upgrades are outside this change.
