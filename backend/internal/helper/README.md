# Protected helper transport

The optional approved helper serves health checks through SDK peer Unix
transport. The package channel is `helper.sock`; the root lifecycle channel is
`helper-root.sock`. Both live in `/var/run/synopkgland/SynoPkgConnect`.
Each admits exactly its selected numeric UID. Health requests still require
the current protected approval and matching inventory. These channels do not
carry lifecycle mutations.

Build a separately approved helper payload with the SDK bindings:

```
-X github.com/synopackageland/syno-pkg-land-sdk/ipc.packageID=SynoPkgConnect
-X github.com/synopackageland/syno-pkg-land-sdk/ipc.executorTier=executor
```

Ordinary CGI and worker payloads bind `executorTier=none`. The current manifest
does not ship this optional helper. A linker binding does not approve, install
or elevate it.

An existing legacy `/run/synopkgland-helper-SynoPkgConnect` runtime requires
approved teardown with its original helper before switching payloads. Status
reports `legacy-runtime` with required cleanup, and the new controller refuses
to start a second helper. Never remove a live runtime directory to bypass this
check.

The supervisor uses `StartPeerUnix` with an exact root policy. The callback
transfers a verified stop request to the OS thread that created the child.
That thread stops and reaps the child, calls `StopAccepting`, writes the full
nonce acknowledgement, and retains the connection until process exit.
`DialPeerUnix` authenticates the root server; the client additionally checks
the supervisor PID and rechecks its protected executable identity. A reply is
accepted only after real connection EOF.

Transport tests require a disposable Linux container. Run the normal backend
tests as a non-root package account with its private `var/run` prepared and
the package/none bindings. The opt-in
`TestPeerTransportHealthAndCurrentApproval` requires root in that disposable
container, a `SynoPkgConnect` account with UID/GID 1000, the package/executor
bindings and `CONNECT_PEER_FIXTURE=1`. It checks root and package peers, rejects
a different UID in the same group, and verifies durable approval withdrawal.
It does not establish NAS acceptance.

For the existing supervisor fixtures, build `testdata/supervised` with package
binding `root` and executor tier `executor` in the disposable root container.
Set `M1C_HELPER_FIXTURE` to that executable and run the helper package's
`TestLinux` tests with the same bindings. Prepare the root-owned
`/var/run/synopkgland` parent first. This exercises twenty normal stop cycles,
child self-exit/reap, supervisor death, identity substitution refusal and
direct/reparented startup refusal against the current transport.

`TestLinuxSupervisorReapsAndStopsAdmissionBeforeACKAndRetainsUntilExit`
additionally takes `M1C_HELPER_ACK_FIXTURE`: the same test executable built
with a test-only overlay that waits for the root-owned `fixture-allow-exit`
marker immediately before the final `os.Exit(0)`. At full ACK, the test
requires the child to have been reaped and admission/socket removal to have
finished. It then requires the connection to remain open while the supervisor
is alive and to reach real EOF only after allowing the process to exit.
