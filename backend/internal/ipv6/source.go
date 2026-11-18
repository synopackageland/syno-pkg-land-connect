package ipv6

import (
	"bufio"
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"strconv"
	"strings"
)

// NativeSource reads the Linux address flags, which net.Interface.Addrs omits.
// Hooks are for local fixtures; the production path is fixed and read-only.
type NativeSource struct {
	Read         func() ([]byte, error)
	UpInterfaces func() (map[string]bool, error)
}

func (s NativeSource) Addresses(ctx context.Context) ([]Address, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	read := s.Read
	if read == nil {
		read = func() ([]byte, error) { return os.ReadFile("/proc/net/if_inet6") }
	}
	raw, err := read()
	if err != nil {
		return nil, err
	}
	addresses, err := ParseAddresses(bytes.NewReader(raw))
	if err != nil {
		return nil, err
	}
	up := s.UpInterfaces
	if up == nil {
		up = func() (map[string]bool, error) {
			interfaces, err := net.Interfaces()
			if err != nil {
				return nil, err
			}
			result := map[string]bool{}
			for _, i := range interfaces {
				result[i.Name] = i.Flags&net.FlagUp != 0 && i.Flags&net.FlagLoopback == 0
			}
			return result, nil
		}
	}
	active, err := up()
	if err != nil {
		return nil, err
	}
	out := make([]Address, 0, len(addresses))
	for _, a := range addresses {
		if active[a.Interface] {
			out = append(out, a)
		}
	}
	return out, ctx.Err()
}

var publicRange = netip.MustParsePrefix("2000::/3")

// Conservative native GUA policy: exclude special protocols, documentation and
// 6to4. Some special-purpose global assignments in 2001::/23 are also excluded.
var excluded = []netip.Prefix{
	netip.MustParsePrefix("2001::/23"), netip.MustParsePrefix("2001:db8::/32"),
	netip.MustParsePrefix("2002::/16"), netip.MustParsePrefix("3fff::/20"),
}

func publicAddress(ip netip.Addr) bool {
	if !ip.Is6() || !publicRange.Contains(ip) {
		return false
	}
	for _, p := range excluded {
		if p.Contains(ip) {
			return false
		}
	}
	return true
}

// ParseAddresses implements Linux /proc/net/if_inet6's six-column hex format.
// The low-byte flags include temporary, optimistic, DAD-failed, deprecated and
// tentative. Permanent is allowed; non-permanent does not imply temporary.
func ParseAddresses(r io.Reader) ([]Address, error) {
	out := []Address{}
	scan := bufio.NewScanner(r)
	for scan.Scan() {
		f := strings.Fields(scan.Text())
		if len(f) == 0 {
			continue
		}
		if len(f) != 6 || len(f[0]) != 32 {
			return nil, errors.New("invalid IPv6 address inventory")
		}
		raw, err := hex.DecodeString(f[0])
		if err != nil {
			return nil, errors.New("invalid IPv6 address inventory")
		}
		values := [4]uint64{}
		for i := range values {
			n, err := strconv.ParseUint(f[i+1], 16, 32)
			if err != nil {
				return nil, errors.New("invalid IPv6 address inventory")
			}
			values[i] = n
		}
		if values[0] == 0 || values[1] > 128 || values[2] > 255 || values[3] > 255 || len(f[5]) > 15 {
			return nil, errors.New("invalid IPv6 address inventory")
		}
		ip := netip.AddrFrom16([16]byte(raw))
		if values[2] != 0 || values[3]&(0x01|0x04|0x08|0x20|0x40) != 0 || !publicAddress(ip) {
			continue
		}
		out = append(out, Address{ip, f[5], int(values[1])})
	}
	if err := scan.Err(); err != nil {
		return nil, err
	}
	return out, nil
}
