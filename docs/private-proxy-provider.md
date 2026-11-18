# Private proxy provider boundary

Tailscale Unix targets require version 1.104.1 or later. HTTPS routes require a
fresh status whose `CertDomains` contains the device's MagicDNS name; Syno Connect Center
refuses an HTTP fallback and withdraws existing web routes if that prerequisite
is lost.

Each approved App supplies a `unix` mapping with an absolute `Socket` under
`pinproxy/` (`appaccess.Engine.PrivateMappings`); the DSM App may too. A
Tailscale connect request carries no mappings, and any socket path in a request
is rejected.

`TailscaleExec.Targets` implements `PrivateTargets.ResolvePrivateTargets`.
It receives a fresh MagicDNS name and the selected mappings. The state owner
must persist each private scope's authority (`<name>:<publicPort>`) and return
its approved socket mapping. It must refuse a changed authority until renewed
administrator confirmation. This hook runs before child reuse, so a retained
pre-upgrade DSM HTTPS child is replaced when its target becomes a Unix socket.
The proxy owns TLS pinning and Host reconstruction; no insecure TLS target is
emitted by the provider.

Public external-access gateway support requires App SDK 0.29.0. Private
forwarding and SDK ping diagnostics have their own discovery requirements.

The daemon wires `ApprovedPrivateTargets` to the persistent App approval store
and fresh DSM HTTPS detection. It resolves `apps.PinBindings` for active private
scopes and requires an exact socket, local port and current MagicDNS authority.
A missing approval or changed authority refuses the mapping and withdraws owned
Serve children; it never preserves a pre-upgrade direct DSM HTTPS child.
