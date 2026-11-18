package cloudflare

import (
	"bytes"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
)

var errCredentials = errors.New("invalid tunnel credentials JSON")

// credentials cannot be exported through a settings/status JSON encoder.
// Formatting is redacted even in accidental diagnostics; only privateJSON
// deliberately produces the private on-disk document for cloudflared.
type credentials struct{ account, tunnelID, secret string }

func (credentials) String() string   { return "[private tunnel credentials]" }
func (credentials) GoString() string { return "[private tunnel credentials]" }
func (c credentials) privateJSON() []byte {
	raw, _ := json.Marshal(struct {
		AccountTag   string
		TunnelID     string
		TunnelSecret string
	}{c.account, c.tunnelID, c.secret})
	return raw
}
func parseCredentials(raw []byte) (credentials, error) {
	if len(raw) == 0 || len(raw) > 4096 {
		return credentials{}, errCredentials
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	first, e := d.Token()
	if e != nil || first != json.Delim('{') {
		return credentials{}, errCredentials
	}
	fields := map[string]string{}
	for d.More() {
		token, e := d.Token()
		name, ok := token.(string)
		if e != nil || !ok {
			return credentials{}, errCredentials
		}
		if _, duplicate := fields[name]; duplicate {
			return credentials{}, errCredentials
		}
		switch name {
		case "AccountTag", "TunnelID", "TunnelSecret", "Endpoint", "TunnelName":
		default:
			return credentials{}, errCredentials
		}
		var value string
		if e = d.Decode(&value); e != nil {
			return credentials{}, errCredentials
		}
		fields[name] = value
	}
	if last, e := d.Token(); e != nil || last != json.Delim('}') {
		return credentials{}, errCredentials
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return credentials{}, errCredentials
	}
	if !accountTag.MatchString(fields["AccountTag"]) || !tunnelUUID.MatchString(fields["TunnelID"]) || fields["TunnelID"] == "00000000-0000-0000-0000-000000000000" || fields["Endpoint"] != "" || len(fields["TunnelName"]) > 256 {
		return credentials{}, errCredentials
	}
	secret, e := base64.StdEncoding.Strict().DecodeString(fields["TunnelSecret"])
	if e != nil || len(secret) != 32 {
		return credentials{}, errCredentials
	}
	return credentials{account: fields["AccountTag"], tunnelID: fields["TunnelID"], secret: base64.StdEncoding.EncodeToString(secret)}, nil
}
