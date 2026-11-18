package ports

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
)

// portConfigLimit bounds the DSM port datastore read.
const portConfigLimit = 65536

// DatastoreDetector reads DSM's current libsynow3 datastore on every poll.
// It uses neither the defaults file nor saved observations. Package accounts
// can read this store without executing the root-only synowebapi program.
// DSM 7.2 added mainApp; DSM 7.1 writes the same ports without it.
type DatastoreDetector struct{ Path string }

func (d DatastoreDetector) Detect(ctx context.Context) (DSMPorts, error) {
	if e := ctx.Err(); e != nil {
		return DSMPorts{}, e
	}
	path := d.Path
	if path == "" {
		path = "/usr/syno/etc/www/DSM.json"
	}
	f, e := openPortConfig(path, portConfigLimit)
	if e != nil {
		return DSMPorts{}, ErrDetection
	}
	defer f.Close()
	raw, e := io.ReadAll(io.LimitReader(f, portConfigLimit+1))
	if e != nil || len(raw) > portConfigLimit || ctx.Err() != nil {
		return DSMPorts{}, ErrDetection
	}
	var v struct {
		MainApp *string `json:"mainApp"`
		Version int     `json:"version"`
		Port    int     `json:"port"`
		SSL     struct {
			Port int `json:"port"`
		} `json:"ssl"`
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	var extra any
	if dec.Decode(&v) != nil || dec.Decode(&extra) != io.EOF || (v.MainApp != nil && *v.MainApp != "DSM") || v.Version != 1 {
		return DSMPorts{}, ErrDetection
	}
	p := DSMPorts{HTTP: v.Port, HTTPS: v.SSL.Port}
	if !validDSM(p) {
		return DSMPorts{}, ErrDetection
	}
	return p, nil
}
