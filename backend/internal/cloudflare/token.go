package cloudflare

import (
	"encoding/base64"
	"encoding/json"
	"regexp"

	"dsmapp/internal/connector"
)

// ErrInvalidToken is safe to show; it never includes the token.
var ErrInvalidToken = connector.Coded("invalid_cloudflare_token", "invalid Cloudflare tunnel token")

var tunnelUUID = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)

// validateToken accepts the token the Cloudflare dashboard shows for a
// tunnel: base64 JSON naming an account, a tunnel and its secret.
func validateToken(token string) error {
	if len(token) > 4096 {
		return ErrInvalidToken
	}
	raw, e := base64.StdEncoding.DecodeString(token)
	if e != nil {
		return ErrInvalidToken
	}
	var t struct {
		Account string `json:"a"`
		Tunnel  string `json:"t"`
		Secret  string `json:"s"`
	}
	if json.Unmarshal(raw, &t) != nil || len(t.Account) != 32 || !tunnelUUID.MatchString(t.Tunnel) {
		return ErrInvalidToken
	}
	// Official cloudflared accepts a secret of at least 32 bytes.
	secret, e := base64.StdEncoding.DecodeString(t.Secret)
	if e != nil || len(secret) < 32 {
		return ErrInvalidToken
	}
	return nil
}
