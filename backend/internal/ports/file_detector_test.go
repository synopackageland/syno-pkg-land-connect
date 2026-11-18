package ports

import (
	"bufio"
	"context"
	"io"
	"strconv"
	"strings"
)

// FileDetector reads only the active configuration. The defaults file, saved
// observations and conventional 5000/5001 values are never fallback sources.
// These keys are a candidate DSM adapter, pending package-account NAS proof.
type FileDetector struct{ Path string }

func (d FileDetector) Detect(ctx context.Context) (DSMPorts, error) {
	if e := ctx.Err(); e != nil {
		return DSMPorts{}, e
	}
	path := d.Path
	if path == "" {
		path = "/etc/synoinfo.conf"
	}
	f, e := openPortConfig(path, 1<<20)
	if e != nil {
		return DSMPorts{}, ErrDetection
	}
	defer f.Close()
	return parsePortConfig(ctx, f)
}

func parsePortConfig(ctx context.Context, r io.Reader) (DSMPorts, error) {
	scanner := bufio.NewScanner(io.LimitReader(r, (1<<20)+1))
	values := map[string]int{}
	for scanner.Scan() {
		key, raw, found := strings.Cut(strings.TrimSpace(scanner.Text()), "=")
		key = strings.TrimSpace(key)
		if key != "admin_port" && key != "secure_admin_port" {
			continue
		}
		if !found {
			return DSMPorts{}, ErrDetection
		}
		if _, ok := values[key]; ok {
			return DSMPorts{}, ErrDetection
		}
		raw = strings.TrimSpace(raw)
		if len(raw) >= 2 && raw[0] == '"' && raw[len(raw)-1] == '"' {
			raw = raw[1 : len(raw)-1]
		}
		if raw == "" {
			return DSMPorts{}, ErrDetection
		}
		for _, c := range raw {
			if c < '0' || c > '9' {
				return DSMPorts{}, ErrDetection
			}
		}
		port, e := strconv.Atoi(raw)
		if e != nil || port < 1 || port > 65535 {
			return DSMPorts{}, ErrDetection
		}
		values[key] = port
	}
	if scanner.Err() != nil || ctx.Err() != nil {
		return DSMPorts{}, ErrDetection
	}
	p := DSMPorts{values["admin_port"], values["secure_admin_port"]}
	if !validDSM(p) {
		return DSMPorts{}, ErrDetection
	}
	return p, nil
}
