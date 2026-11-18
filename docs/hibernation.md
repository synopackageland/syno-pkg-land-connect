# Persistent writes and runtime storage

Syno Connect Center's five-second observation is now a disposable latest-state file in a
0700 package-account directory on `/tmp` tmpfs. No observation is written to the
package data directory or the system disk. Freshness/revision/plan validation
is unchanged; a reboot discards observations and the daemon publishes a new one.
Linux refuses non-tmpfs `/tmp` instead of silently using persistent storage.

Provider audit:

- connectd: desired port/provider settings use atomic writes on changes, with no
  periodic durable heartbeat. The SDK supervisor's daemon log is persistent;
  Syno Connect Center emits lifecycle/failure messages there, not per-tick success logs.
- cloudflared: child output goes to a bounded in-memory parser or is discarded;
  no logfile is configured. Identity/certificates and generated ingress settings
  remain durable, written during explicit connection/setup changes.
- tailscaled: `--statedir` and `TS_LOGS_DIR` use private tmpfs. The existing
  `TS_NO_LOGS_NO_SUPPORT=true` stays set. `--state` remains the durable identity
  file so a reboot does not require login again. Upstream owns its state writes;
  key rotation, account/network changes or explicit configuration may write it.
- NetBird: `NB_STATE_DIR` uses private tmpfs. `--log-file console` output is
  discarded, and `client/default.json` keeps the durable identity. The official
  state manager checks every ten seconds but saves only dirty state; that state
  is now volatile. Identity/configuration updates may still write the durable file.

The tmpfs change deliberately does not erase persistent identities or move their
only copy into RAM. Active traffic, authentication/key rotation, reconnects,
package lifecycle, user changes and runtime failures are outside a steady idle
zero-write window. A disconnected test does not certify an authenticated
provider's upstream behavior or actual disk hibernation.

Upstream references:

- [Tailscale 1.104.5 log storage](https://github.com/tailscale/tailscale/blob/v1.104.5/logpolicy/logpolicy.go)
- [NetBird 0.82.0 state manager](https://github.com/netbirdio/netbird/blob/v0.82.0/client/internal/statemanager/manager.go)

A five-second tick still uses CPU for cached reads and metadata checks; it does
not spin down the disk itself. Test with per-process `/proc/<pid>/io` deltas plus
file metadata on the actual NAS, with no user mutations during the idle window.


## Provider storage ceilings (0136)

The worker execs official Tailscale/NetBird children through an unprivileged
trampoline with a 1 MiB hard RLIMIT_FSIZE and zero core-file limit. This applies
to temporary and identity files, including atomic-write part files. An oversized
write fails instead of filling tmpfs; the process may exit and normal supervision
handles that exit. A large state/network configuration may therefore require
operator attention. The worker itself retains its ordinary limits for downloads.

Taildrop is disabled (`TS_DISABLE_TAILDROP=true`); this App has no file-receive
feature. Network-map disk caching is disabled (`TS_USE_CACHED_NETMAP=false`).
Provider TMPDIR is its private tmpfs tree. Stop joins the children before removing
that tree, and a fresh start removes stale backups from an earlier crash. Both
uninstall paths clear transient trees while retaining identities as requested.
The observation writer refuses encoded state larger than 64 KiB before opening
a temporary file; failures retain the previous complete observation.

0136's per-file limit and start/stop cleanup do not provide an aggregate quota.
The user accepted 0136 with the following retention work split into 0137.

## Aggregate retention (0137)

These conclusions are **static / high confidence**, based on the pinned official
Tailscale 1.104.5 and NetBird 0.82.0 sources plus local regression tests. Neither
provider was started on the test NAS.

Both providers clear their private tmpfs tree before a new session and after
joining children on stop. Uninstall also removes these trees. During a session,
a 15-second timer validates and scans only the owned 0700 `/tmp` tree; it never
resolves or reads persistent identity paths. No root, FUSE or user namespaces
are used. The timer belongs to the running daemon's session and observes the
SDK process Done channel: daemon exit, failure or Stop ends it. Disabled or
not-started providers create no timer. A running but traffic-idle provider still
needs this tmpfs-only sweep; it performs no persistent-volume IO.

| Data | Retention |
| --- | --- |
| NetBird `state.json.corrupted.*` | Keep the two newest by modification time |
| NetBird hidden JSON error parts | Remove parts older than five minutes; retain current writes |
| Tailscale `<name>.tmp<digits>` atomic-write parts | Remove leftovers older than five minutes; retain fresh writes |
| Tailscale `certs` | Evict oldest certificate/key pairs above 2 MiB or 64 files; keep `acme-account.key.pem` |
| Tailscale `dns-cache` | Evict oldest files above 512 KiB or 32 files |
| All other private runtime files, including Tailnet Lock history | After safe eviction, stop if still above 8 MiB of accounted files or 256 entries; never truncate security history |

Before stopping for overall pressure, evict additional oldest DNS entries and
certificate/key pairs even when they fit their ordinary cache targets. Preserve
the ACME account and active parts. Old NetBird corrupt backups can yield below
the usual two-copy target, and the regenerable Tailscale DERP map can be removed.
Only state still over budget after these evictions causes
`tmpfs_budget_exceeded`. The UI names Tailscale or NetBird, explains that temporary
files still exceed the limit after cache cleanup, and offers one **Restart**
action. It uses the existing saved-settings Retry route: clean the private runtime
tree, start a new daemon and reuse persistent identity. A failed retry keeps the
cause and the action available. Unsafe/unreadable storage has a distinct
`tmpfs_storage_unavailable` cause instead of being mislabeled as overflow.

Accounting charges each regular file the larger of logical bytes and allocated
512-byte blocks. Entry counting includes directories. A scan refuses symlinks,
unexpected file types or more than 4096 entries. Fresh atomic-write cache parts
are counted but not evicted. A storage failure is recorded as permanent
`tmpfs_budget_exceeded`, so supervision cleans up instead of silently restarting.
Normal stop joins the timer before stopping children and removing runtime files.

### Expected normal footprint and headroom

**Static / moderate confidence, engineering estimates, not NAS measurements:**
for one default profile serving DSM and a few App routes, with Taildrop, file
logging and disk netmap caching disabled, expect Tailscale runtime files to be
well below 1 MiB and 64 entries (small log-ID configs, DERP/DNS records, ACME
account and a few PEM pairs). NetBird normally has a few JSON state/prefs/hint
files plus its seven-byte profile selector: expect below 64 KiB and 16 entries.
These estimates count filesystem allocation rather than just JSON text.

The 8 MiB / 256-entry budget gives at least 8x byte / 4x entry headroom over
that conservative Tailscale estimate, and 128x / 16x over NetBird's. Even with
both Tailscale cache byte targets filled, their 2.5 MiB plus small fixed state
is roughly 2.6 MiB and 100–110 entries, leaving about 3x byte and 2x entry
headroom. Upstream does not promise these normal sizes for every network.
Large externally enabled Tailnet Lock history, oversized recovery state and
unusual numbers of routes/hostnames remain exceptional growth paths; eviction
and the actionable overflow stop cover them. Their size cannot be certified
from a disconnected static analysis.

This is periodic pruning and an overflow stop, **not an instantaneous filesystem
quota**: writers may exceed the aggregate thresholds between scans. Each child
still has the 1 MiB hard per-file limit. Directory metadata and transient
unlinked open files are not included in the byte total. No data is moved onto a
persistent volume to meet these budgets.

NetBird's `config/netbird/active_profile.txt` remains an official direct
`os.WriteFile` (**static / high confidence**), rather than atomic replacement.
With the fixed default profile and no product profile selector, its content is
the seven-byte `default`; it is not an append log or a growing history. Making
upstream's in-process write atomic would require an upstream change, so the
accepted small fixed-size exception is documented. The ordinary child file
ceiling and stop/uninstall cleanup also apply. Persistent identities remain at
`connect/tailscale/tailscaled.state` and `connect/netbird/client/default.json`;
authentication/configuration/key changes can write them. This static analysis
does not establish authenticated idle IO or physical disk hibernation.
