package cfaccess

import (
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"strings"
	"syscall"

	"dsmapp/internal/connector"
)

type record struct {
	Session        string `json:"session_duration,omitempty"`
	PendingSession string `json:"pending_session_duration,omitempty"`
	Creating       bool   `json:"create_sent,omitempty"`
	Digest         string `json:"digest,omitempty"`
	Name           string `json:"name"`
	ID             string `json:"id,omitempty"`
	Policy         string `json:"policy,omitempty"`
	AUD            string `json:"aud,omitempty"`
	Revoke         bool   `json:"revoke,omitempty"`
}
type state struct {
	Session    string            `json:"session_duration,omitempty"`
	DSMHost    string            `json:"dsm_host,omitempty"`
	OTPPending bool              `json:"otp_create_sent,omitempty"`
	Schema     int               `json:"schema"`
	Owner      string            `json:"owner"`
	Account    string            `json:"account,omitempty"`
	Revision   uint64            `json:"revision"`
	Emails     []string          `json:"emails"`
	Records    map[string]record `json:"records"`
	OTP        string            `json:"otp,omitempty"`
	OTPName    string            `json:"otp_name,omitempty"`
	OwnedOTP   bool              `json:"owned_otp,omitempty"`
	Issuer     string            `json:"issuer,omitempty"`
	Synced     bool              `json:"synced"`
	Checked    int64             `json:"checked,omitempty"`
	Code       string            `json:"code,omitempty"`
}

func randomID() (string, error) {
	var b [16]byte
	if _, e := rand.Read(b[:]); e != nil {
		return "", failure("access_unavailable")
	}
	return hex.EncodeToString(b[:]), nil
}
func load(path string) (state, error) {
	v := state{Schema: 1, Emails: []string{}, Records: map[string]record{}}
	f, e := os.OpenFile(path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(e, os.ErrNotExist) {
		return v, nil
	}
	if e != nil {
		return v, failure("access_state_corrupt")
	}
	defer f.Close()
	st, e := f.Stat()
	if e != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 || st.Size() > 1<<20 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return v, failure("access_state_corrupt")
	}
	raw, e := io.ReadAll(io.LimitReader(f, 1<<20+1))
	if e != nil || json.Unmarshal(raw, &v) != nil || v.Schema != 1 || len(v.Owner) != 32 || v.Records == nil || len(v.Records) > 1024 {
		return v, failure("access_state_corrupt")
	}
	if _, e = Emails(v.Emails); e != nil {
		return v, failure("access_state_corrupt")
	}
	for host, r := range v.Records {
		if !validHost(host) || !stringsOwner(r.Name, v.Owner) {
			return v, failure("access_state_corrupt")
		}
	}
	return v, nil
}
func stringsOwner(name, owner string) bool {
	return len(owner) == 32 && strings.HasPrefix(name, "Connect "+owner+" ") && len(name) > len("Connect "+owner+" ")
}
func save(path string, v state) error {
	dir := filepath.Dir(path)
	if os.MkdirAll(dir, 0700) != nil {
		return failure("access_state_corrupt")
	}
	st, e := os.Lstat(dir)
	if e != nil || !st.IsDir() || st.Mode().Perm() != 0700 || st.Sys().(*syscall.Stat_t).Uid != uint32(os.Geteuid()) {
		return failure("access_state_corrupt")
	}
	raw, e := json.Marshal(v)
	if e != nil || len(raw) > 1<<20 || connector.WritePrivate(path, raw) != nil {
		return failure("access_state_corrupt")
	}
	return nil
}
