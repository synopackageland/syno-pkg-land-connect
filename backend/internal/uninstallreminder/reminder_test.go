package uninstallreminder

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const base = `[{"step_title":"SDK data choices","items":[{"type":"multiselect","subitems":[{"key":"pkgwizard_remove_additional","defaultValue":false}]}]}]`

func save(t *testing.T, dir, path, raw string) {
	t.Helper()
	file := filepath.Join(dir, path)
	if err := os.MkdirAll(filepath.Dir(file), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(file, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
}
func render(t *testing.T, dir string) string {
	t.Helper()
	raw, err := Render([]byte(base), dir, "en")
	if err != nil {
		t.Fatal(err)
	}
	var parsed []any
	if json.Unmarshal(raw, &parsed) != nil || len(parsed) != 2 {
		t.Fatal(string(raw))
	}
	return string(raw)
}
func TestUsedProvidersAndSecrets(t *testing.T) {
	dir := t.TempDir()
	save(t, dir, "workers/state.json", `{"token":"SECRET-TOKEN","account_id":"SECRET-ACCOUNT","owner":"SECRET-OWNER","records":{"dsm":{"name":"dsm-example","secret":"SECRET-WORKER"},"app":{"name":"example-app","deployed":false}},"signal_bridge":{"zone":"example.com","host":"netbird.example.com","script":"connect-nbsignal-0123456789ab"}}`)
	save(t, dir, "cloudflare/domain/domain.json", `{"hostname":"nas.example.com","tunnelId":"11111111-2222-3333-4444-555555555555","routed":["files.example.com"],"services":[{"hostname":"drive.example.com"}]}`)
	save(t, dir, "netbird/settings.json", `{"enabled":false,"settings":{"managementUrl":"https://netbird.example.com:443","token":"SECRET-SETUP"}}`)
	out := render(t, dir)
	for _, want := range []string{"/?to=/:account/workers-and-pages", "/?to=/:account/:zone/dns/records", "/?to=/:account/access/tunnels", "dsm-example", "example-app", "connect-nbsignal-0123456789ab", "nas.example.com", "files.example.com", "drive.example.com", "11111111-2222-3333-4444-555555555555", "https://netbird.example.com:443/peers"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s", want)
		}
	}
	for _, no := range []string{"SECRET-", "Tailscale", "app.netbird.io"} {
		if strings.Contains(out, no) {
			t.Fatalf("unexpected %s", no)
		}
	}
	// Replacing the final page never duplicates it or changes SDK data choices.
	again, err := Render([]byte(out), dir, "en")
	if err != nil || string(again) != out {
		t.Fatal("not repeatable", err)
	}
	original, _ := os.ReadFile(filepath.Join(dir, "workers/state.json"))
	if !strings.Contains(string(original), "SECRET-TOKEN") {
		t.Fatal("state changed")
	}
}
func TestProviderSelection(t *testing.T) {
	for _, name := range []string{"tailscale", "netbird"} {
		t.Run(name, func(t *testing.T) {
			dir := t.TempDir()
			save(t, dir, name+"/settings.json", `{"settings":{}}`)
			out := render(t, dir)
			want := "Tailscale"
			other := "NetBird"
			if name == "netbird" {
				want, other = other, want
			}
			if !strings.Contains(out, want) || strings.Contains(out, other) || strings.Contains(out, "Cloudflare") {
				t.Fatal(out)
			}
		})
	}
	dir := t.TempDir()
	save(t, dir, "cloudflare/settings.json", `{"mode":"quick"}`)
	save(t, dir, "tailscale/settings.json", `{"enabled":false,"settings":{}}`)
	if out := render(t, dir); strings.Contains(out, "Cloudflare") || !strings.Contains(out, "synopkgconnect") {
		t.Fatal(out)
	}
}
func TestFallbackForMissingMalformedUnreadableAndUnsafeState(t *testing.T) {
	for _, kind := range []string{"missing", "empty", "malformed", "permissions", "symlink", "fifo", "directory", "oversized"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			path := "tailscale/settings.json"
			switch kind {
			case "missing":
				dir = filepath.Join(dir, "missing")
			case "empty":
			default:
				save(t, dir, path, `{"settings":{}}`)
				file := filepath.Join(dir, path)
				switch kind {
				case "malformed":
					save(t, dir, path, `{"token":"SECRET-BROKEN"`)
				case "permissions":
					if err := os.Chmod(file, 0000); err != nil {
						t.Fatal(err)
					}
				case "symlink":
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
					if err := os.Symlink("/dev/null", file); err != nil {
						t.Fatal(err)
					}
				case "fifo":
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
					makeFIFO(t, file)
				case "directory":
					if err := os.Remove(file); err != nil {
						t.Fatal(err)
					}
					if err := os.Mkdir(file, 0700); err != nil {
						t.Fatal(err)
					}
				case "oversized":
					save(t, dir, path, strings.Repeat("x", (1<<20)+1))
				}
			}
			out := render(t, dir)
			for _, want := range []string{"Cloudflare", "Tailscale", "NetBird"} {
				if !strings.Contains(out, want) {
					t.Fatal(out)
				}
			}
			if strings.Contains(out, "SECRET") {
				t.Fatal(out)
			}
		})
	}
}
func TestLocalesAndUntrustedDisplayValues(t *testing.T) {
	var catalog map[string]map[string]string
	if err := json.Unmarshal(messages, &catalog); err != nil {
		t.Fatal(err)
	}
	if len(catalog) != 9 {
		t.Fatal("nine locales required")
	}
	for locale, copy := range catalog {
		t.Run(locale, func(t *testing.T) {
			if len(copy) != len(catalog["en"]) {
				t.Fatal("key mismatch")
			}
			for key := range catalog["en"] {
				v := copy[key]
				if v == "" || strings.ContainsAny(v[len(v)-1:], ".") || strings.HasSuffix(v, "。") {
					t.Fatalf("bad copy %s", key)
				}
			}
			raw, err := Render([]byte(base), "", locale)
			if err != nil || !json.Valid(raw) {
				t.Fatal(err)
			}
			var pages []json.RawMessage
			_ = json.Unmarshal(raw, &pages)
			if string(pages[0]) != base[1:len(base)-1] { // JSON indentation is semantically irrelevant.
				var original, got any
				_ = json.Unmarshal([]byte(base[1:len(base)-1]), &original)
				_ = json.Unmarshal(pages[0], &got)
				a, _ := json.Marshal(original)
				b, _ := json.Marshal(got)
				if string(a) != string(b) {
					t.Fatal("SDK page changed")
				}
			}
		})
	}
	dir := t.TempDir()
	save(t, dir, "workers/state.json", `{"records":{"x":{"name":"<script>alert(1)</script>"}}}`)
	save(t, dir, "netbird/settings.json", `{"settings":{"managementUrl":"https://user:SECRET@example.com/SECRET?token=SECRET"}}`)
	out := render(t, dir)
	if strings.Contains(out, "SECRET") || strings.Contains(out, "script") || strings.Contains(out, "user:") {
		t.Fatal(out)
	}
}

func TestKeptCredentialsServerAndIPv6AreNamedNotRead(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "connect")
	save(t, dir, "netbird-server/state.json", `{"enabled":false,"hostname":"netbird.example.com","routed":true}`)
	save(t, dir, "netbird-server/secrets.json", `{"adminPassword":"SECRET-PASSWORD"}`)
	save(t, dir, "account-token/credential.json", `{"token":"SECRET-ACCOUNT"}`)
	save(t, dir, "tailscale/tailscaled.state", `SECRET-NODE`)
	save(t, dir, "netbird/client/default.json", `SECRET-PEER`)
	save(t, dir, "ipv6.json", `{"enabled":false,"mappings":[{"localPort":5001,"protocol":"https","publicPort":5001}]}`)
	save(t, root, "github-token", `SECRET-GITHUB`)
	out := render(t, dir)
	for _, want := range []string{"netbird.example.com", "Cloudflare account API token", "NetBird server data", "Tailscale device key", "NetBird peer keys", "GitHub token", "IPv6"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %s: %s", want, out)
		}
	}
	if strings.Contains(out, "SECRET") {
		t.Fatal(out)
	}
	// Nothing kept, nothing listed.
	empty := t.TempDir()
	save(t, empty, "tailscale/settings.json", `{"settings":{}}`)
	if out = render(t, empty); strings.Contains(out, "Kept on this NAS") || strings.Contains(out, "IPv6") {
		t.Fatal(out)
	}
}
