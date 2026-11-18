package cloudflare

import (
	"encoding/json"
	"encoding/pem"
	"errors"
	"io"
	"os"
	"regexp"
	"syscall"
)

var errCert = errors.New("the Cloudflare sign-in certificate is not usable")
var accountTag = regexp.MustCompile(`^[0-9a-f]{32}$`)

// cert is the account-wide tunnel authorization written by cloudflared login.
// It is retained as a private 0600 file for later DNS operations. Formatting
// redacts the API token; explicit disconnect and disable remove the file.
type cert struct{ zoneID, accountID, apiToken string }

func (cert) String() string   { return "[private Cloudflare certificate]" }
func (cert) GoString() string { return "[private Cloudflare certificate]" }

// readCert reads cert.pem only as this account's private regular file and
// only in the shape official cloudflared writes: one ARGO TUNNEL TOKEN block.
func readCert(path string) (cert, error) {
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if e != nil {
		return cert{}, errCert
	}
	defer f.Close()
	info, e := f.Stat()
	if e != nil || !info.Mode().IsRegular() || info.Mode().Perm() != 0600 || info.Size() > 16384 {
		return cert{}, errCert
	}
	if st, ok := info.Sys().(*syscall.Stat_t); !ok || int(st.Uid) != os.Getuid() {
		return cert{}, errCert
	}
	raw, e := io.ReadAll(io.LimitReader(f, 16385))
	if e != nil || len(raw) > 16384 {
		return cert{}, errCert
	}
	var found *cert
	for block, rest := pem.Decode(raw); block != nil; block, rest = pem.Decode(rest) {
		if block.Type != "ARGO TUNNEL TOKEN" || found != nil {
			return cert{}, errCert
		}
		var token struct {
			ZoneID    string `json:"zoneID"`
			AccountID string `json:"accountID"`
			APIToken  string `json:"apiToken"`
			Endpoint  string `json:"endpoint"`
		}
		if json.Unmarshal(block.Bytes, &token) != nil || !accountTag.MatchString(token.ZoneID) || !accountTag.MatchString(token.AccountID) || token.APIToken == "" || token.Endpoint != "" {
			return cert{}, errCert
		}
		found = &cert{token.ZoneID, token.AccountID, token.APIToken}
	}
	if found == nil {
		return cert{}, errCert
	}
	return *found, nil
}
