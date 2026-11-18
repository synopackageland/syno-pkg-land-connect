package provider

import (
	"bytes"
	"crypto/sha256"
	"dsmapp/internal/connector"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
)

// NetBird keeps one peer identity per management server. The active one is
// client/ (the daemon's config and WireGuard key); identity.json names its
// management server. Switching servers parks the active identity under
// identities/<hash> and brings back the new server's one, so returning to a
// server resumes the same peer instead of registering a duplicate.

// A broken identity store is a start failure the page already explains.
var errNetBirdIdentity = ErrNetBirdStart

type netbirdIdentityOwner struct {
	ManagementURL string `json:"managementUrl"`
}

func (p *NetBird) hasIdentity() bool {
	info, e := os.Lstat(filepath.Join(p.dir, "client", "default.json"))
	return e == nil && info.Mode().IsRegular() && info.Size() > 0
}

func netbirdIdentitySlot(management string) string {
	sum := sha256.Sum256([]byte(management))
	return hex.EncodeToString(sum[:12])
}

// identityOwner reads which management server client/ belongs to. Before
// identity.json existed the saved settings named it.
func (p *NetBird) identityOwner() (string, error) {
	raw, e := connector.ReadPrivate(filepath.Join(p.dir, "identity.json"))
	if e == nil {
		var v netbirdIdentityOwner
		d := json.NewDecoder(bytes.NewReader(raw))
		d.DisallowUnknownFields()
		var extra any
		if d.Decode(&v) != nil || d.Decode(&extra) != io.EOF {
			return "", errNetBirdIdentity
		}
		return v.ManagementURL, nil
	}
	if !os.IsNotExist(e) {
		return "", errNetBirdIdentity
	}
	saved := p.saved
	if saved.Settings.ManagementURL == "" {
		if raw, e := connector.ReadPrivate(filepath.Join(p.dir, "settings.json")); e == nil {
			_ = json.Unmarshal(raw, &saved)
		}
	}
	owner, _ := netbirdManagementURL(saved.Settings.ManagementURL)
	return owner, nil
}

// selectIdentity makes client/ the identity of management before a start.
func (p *NetBird) selectIdentity(management string) error {
	owner, e := p.identityOwner()
	if e != nil {
		return e
	}
	client := filepath.Join(p.dir, "client")
	if owner != management {
		parked := filepath.Join(p.dir, "identities")
		if e = os.MkdirAll(parked, 0700); e != nil {
			return errNetBirdIdentity
		}
		if owner != "" && p.hasIdentity() {
			slot := filepath.Join(parked, netbirdIdentitySlot(owner))
			if e = os.RemoveAll(slot); e != nil {
				return errNetBirdIdentity
			}
			if e = os.Rename(client, slot); e != nil {
				return errNetBirdIdentity
			}
		} else if e = os.RemoveAll(client); e != nil {
			return errNetBirdIdentity
		}
		slot := filepath.Join(parked, netbirdIdentitySlot(management))
		if e = os.Rename(slot, client); e != nil && !errors.Is(e, os.ErrNotExist) {
			return errNetBirdIdentity
		}
	}
	raw, _ := json.Marshal(netbirdIdentityOwner{ManagementURL: management})
	if e = os.MkdirAll(p.dir, 0700); e != nil {
		return errNetBirdIdentity
	}
	if connector.WritePrivate(filepath.Join(p.dir, "identity.json"), raw) != nil {
		return errNetBirdIdentity
	}
	return nil
}

// registeredMarker records in an identity's directory that it signed in at
// least once, so Off can say the device is still registered. The key files
// alone do not tell: both daemons save a key before the first sign-in.
const registeredMarker = "registered"

func registered(dir string) bool {
	info, e := os.Lstat(filepath.Join(dir, registeredMarker))
	return e == nil && info.Mode().IsRegular()
}

func markRegistered(dir string) {
	if registered(dir) {
		return
	}
	_ = connector.WritePrivate(filepath.Join(dir, registeredMarker), nil)
}

func nonEmptyFile(path string) bool {
	info, e := os.Lstat(path)
	return e == nil && info.Mode().IsRegular() && info.Size() > 0
}
