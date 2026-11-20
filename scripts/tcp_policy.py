"""The SDK's TCP listener policy for binaries this repository builds itself.

ipc.ListenTCP opens only listeners named in a policy linked into the binary
(-X ...ipc.tcpListenersPolicy). The CLI links it into the binaries it builds;
connectd and connect-worker are built by scripts/prepare.py, so they need the
same flag, encoded the same way: base64url (no padding) of compact JSON
{"schema":1,"listeners":{<id>:{"scope","port"[,"port_range"]}}}, ids sorted.
tests/build/test_tcp_policy.py checks the result against the CLI's own encoder.
"""
import base64, json, re

FLAG = '-X github.com/synopackageland/syno-pkg-land-sdk/ipc.tcpListenersPolicy='
ID = re.compile(r'^[a-z][a-z0-9_]{0,63}$')


def listeners(manifest):
    """tcp_listeners from dsm-package.yaml text (block style), or None when absent."""
    lines = manifest.splitlines()
    try:
        start = next(i for i, line in enumerate(lines) if re.match(r'^tcp_listeners:\s*(#.*)?$', line))
    except StopIteration:
        return None
    result, current = {}, None
    for line in lines[start + 1:]:
        if not line.strip() or line.lstrip().startswith('#'):
            continue
        if not line.startswith(' '):
            break
        m = re.match(r'^  ([^\s:#]+):\s*(#.*)?$', line)
        if m:
            current = m.group(1)
            if not ID.match(current) or current in result:
                raise ValueError(f'tcp_listeners: invalid or repeated id {current!r}')
            result[current] = {}
            continue
        m = re.match(r'^    (scope|port|port_range):\s*(.+?)\s*(#.*)?$', line)
        if not m or current is None:
            raise ValueError(f'tcp_listeners: unsupported line {line.strip()!r}')
        key, value = m.group(1), m.group(2)
        if key == 'scope':
            result[current][key] = value.strip('"\'')
        elif key == 'port':
            result[current][key] = int(value)
        else:
            r = re.match(r'^\[\s*(\d+)\s*,\s*(\d+)\s*\]$', value)
            if not r:
                raise ValueError('tcp_listeners: port_range must be [min, max]')
            result[current][key] = [int(r.group(1)), int(r.group(2))]
    if not result:
        raise ValueError('tcp_listeners: no listeners')
    out = {}
    for name in sorted(result):
        item = result[name]
        scope = item.get('scope', 'loopback')
        port = item.get('port')
        if scope not in ('loopback', 'all') or not isinstance(port, int) or not 1024 <= port <= 65535:
            raise ValueError(f'tcp_listeners.{name}: needs scope loopback or all and a port from 1024 to 65535')
        out[name] = {'scope': scope, 'port': port, **({'port_range': item['port_range']} if 'port_range' in item else {})}
    return out


def flag(manifest):
    """The linker flag for the manifest's listeners, or '' when it declares none."""
    found = listeners(manifest)
    if not found:
        return ''
    raw = json.dumps({'schema': 1, 'listeners': found}, separators=(',', ':')).encode()
    return FLAG + base64.urlsafe_b64encode(raw).decode().rstrip('=')
