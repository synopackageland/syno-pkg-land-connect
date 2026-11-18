# Idle supervisor memory

`connectd serve` is a small package-account supervisor. It holds the exclusive
lifetime lock, passes that lock and a parent-death pipe to the sibling
`connect-worker work`, forwards stop signals and applies the shared SDK restart
policy. It imports the SDK supervision package, with no HTTP, TLS, App or provider
initialization graph. Other connectd
commands replace themselves with connect-worker using exec. Both executables
are built together into each architecture's daemon bundle.

The worker owns the private control socket and App/provider state. Its built-in
Mozilla CA pool is parsed once, on the first web-PKI verification; an idle worker
or an SPKI-only binding does not parse it. Injected test roots remain isolated.
Unused providers start no child, monitoring loop or ticker. Recovery follows [the shared restart policy](restart-policy.md); dependency waiting
does not spend failures and terminal failure retains no retry timer.

## Linux measurements

Measured against `71edbff` using Go 1.26.10, Linux amd64, CGO disabled, production
`-trimpath -buildvcs=false -ldflags='-s -w'` builds, three fresh empty-state runs,
three seconds after the control socket appeared. No provider was enabled.
These container measurements do not establish DSM RSS or provider throughput.
Confidence: high for these samples; unknown for NAS results.

| Metric | Before | After |
| --- | ---: | ---: |
| Supervisor RSS, median | 9,408 KiB | 3,116 KiB |
| Supervisor anonymous RSS, median | 2,768 KiB | 976 KiB |
| Supervisor file RSS, median | 6,640 KiB | 2,140 KiB |
| Supervisor PSS, median | 6,086 KiB | 3,112 KiB |
| Worker RSS, median | 11,224 KiB | 10,296 KiB |
| Combined supervisor + worker PSS, median of paired samples | 14,068 KiB | 13,332 KiB |
| Supervisor stripped binary | 9,478,306 bytes | 2,220,194 bytes |
| Supervisor imported packages | 226 | 75 |
| Worker stripped binary | Same executable | 9,474,210 bytes |

Supervisor RSS fell 67%; combined PSS fell about 5%. Separate executables lose
shared text pages, so the supervisor's RSS saving is not an equivalent reduction
in total physical memory. The additional executable costs about 2.22 MB on disk.
Candidate supervisor RSS ranged from 3,032 to 3,184 KiB in the three samples.

Runtime stats were collected from separate, disposable builds with a Go overlay
that adds a signal-triggered probe. The probe goroutine is subtracted from the
counts. These stats must not be confused with the uninstrumented RSS samples.

| Runtime metric | Supervisor before → after | Worker before → after |
| --- | ---: | ---: |
| HeapAlloc | 1,335,992 → 257,544 bytes | 2,199,048 → 1,532,984 bytes |
| HeapInuse | 2,228,224 → 573,440 bytes | 3,407,872 → 2,547,712 bytes |
| StackInuse | 720,896 → 458,752 bytes | 851,968 → 819,200 bytes |
| Go Sys | 13,064,456 → 8,083,720 bytes | 12,802,312 → 13,064,456 bytes |
| Goroutines | 3 → 3 | 4 → 4 |

The supervisor's three goroutines are the main wait, signal receiver and child
wait. It has two one-slot channels and kernel pipe/file descriptors, with no
provider or HTTP payload buffers. The worker's fourth goroutine waits on the
parent pipe using an 8 KiB discard buffer; its other goroutines wait for control
requests, signals and shutdown. No provider child or provider loop appeared in
the idle samples. HTTP/proxy payload buffers are created on demand.

Most resident bytes are executable/runtime pages, not retained application heap.
Splitting removes the full App/provider initialization graph from the supervisor;
deferring CA parsing avoids roughly 8,700 constructor allocations in the idle worker.
Go's reserved `Sys`/heap address space is not RSS and varies with scheduling.

A one-run experiment with `GOGC=50 GOMEMLIMIT=8MiB` reduced the worker's reported
heap but increased combined PSS to 14,336 KiB and triggered extra collections.
No GC limit, forced-GC loop or processor limit is shipped: that sample did not
justify their CPU/latency cost. There is no new background sampler.

## Reproduce and accept

On a Linux build host with the pinned Go toolchain and module dependencies:

```sh
python3 scripts/measure-connect-memory.py --output /tmp/connect-memory
```

The script builds production binaries and separate instrumented binaries, keeps
all state private in a temporary directory, records both processes' `/proc`
RSS/PSS and Go stats, and stops them with the package controller's SIGTERM path.
Its probe and stack dump are never included in shipped executables. Use the same
script on the baseline checkout for comparisons. The output includes import
lists and goroutine stacks. Do not add the two RSS values to estimate physical
memory; use the paired PSS total.

Later DSM acceptance must verify the two executable payloads on each supported
architecture, idle RSS/PSS with no provider children/loops, slow restore and
health tolerance, duplicate-start exclusion, worker-crash backoff, supervisor
death cleanup, stop/start, upgrade intent retention and offline uninstall revoke.
Check a first web-PKI App connection, concurrent handshakes and SPKI-only access;
confirm the deferred pool preserves certificate rejection and provider behavior.

The normal related Go tests and lifecycle scenarios passed locally. An additional
race-instrumented 50-client throughput stress test timed out under build-host
load on both the unchanged baseline and this candidate (`context deadline
exceeded`); it is not a passing throughput acceptance result. Certificate-policy
and concurrent first-use tests are checked separately with the race detector.
