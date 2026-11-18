package cloudflare

import (
	"encoding/pem"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const testZone = "0123456789abcdef0123456789abcdef"
const testAccount = "fedcba9876543210fedcba9876543210"

func certPEM(body string) []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "ARGO TUNNEL TOKEN", Bytes: []byte(body)})
}
func goodCert() []byte {
	return certPEM(fmt.Sprintf(`{"zoneID":%q,"accountID":%q,"apiToken":"secret-api-token"}`, testZone, testAccount))
}
func writeCert(t *testing.T, raw []byte, mode os.FileMode) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cert.pem")
	if e := os.WriteFile(path, raw, mode); e != nil {
		t.Fatal(e)
	}
	if e := os.Chmod(path, mode); e != nil {
		t.Fatal(e)
	}
	return path
}

func TestReadCertAcceptsAPrivateOfficialCertificate(t *testing.T) {
	c, e := readCert(writeCert(t, goodCert(), 0600))
	if e != nil {
		t.Fatal(e)
	}
	if c.zoneID != testZone || c.accountID != testAccount || c.apiToken != "secret-api-token" {
		t.Fatalf("cert = %#v", c)
	}
	for _, shown := range []string{fmt.Sprint(c), fmt.Sprintf("%#v", c), fmt.Sprintf("%+v", c)} {
		if strings.Contains(shown, "secret-api-token") {
			t.Fatalf("certificate printed its token: %s", shown)
		}
	}
}

func TestReadCertRefusesAnythingButAPrivateRegularCertificate(t *testing.T) {
	big := append(goodCert(), make([]byte, 20000)...)
	cases := map[string]func(t *testing.T) string{
		"world readable": func(t *testing.T) string { return writeCert(t, goodCert(), 0644) },
		"symlink": func(t *testing.T) string {
			target := writeCert(t, goodCert(), 0600)
			link := filepath.Join(t.TempDir(), "cert.pem")
			if e := os.Symlink(target, link); e != nil {
				t.Fatal(e)
			}
			return link
		},
		"directory": func(t *testing.T) string { return t.TempDir() },
		"missing":   func(t *testing.T) string { return filepath.Join(t.TempDir(), "cert.pem") },
		"too large": func(t *testing.T) string { return writeCert(t, big, 0600) },
		"no token":  func(t *testing.T) string { return writeCert(t, certPEM(`{"zoneID":"`+testZone+`"}`), 0600) },
		"bad zone": func(t *testing.T) string {
			return writeCert(t, certPEM(`{"zoneID":"zone","accountID":"`+testAccount+`","apiToken":"x"}`), 0600)
		},
		"bad account": func(t *testing.T) string {
			return writeCert(t, certPEM(`{"zoneID":"`+testZone+`","accountID":"a","apiToken":"x"}`), 0600)
		},
		"fedramp": func(t *testing.T) string {
			return writeCert(t, certPEM(`{"zoneID":"`+testZone+`","accountID":"`+testAccount+`","apiToken":"x","endpoint":"fed"}`), 0600)
		},
		"unknown block": func(t *testing.T) string {
			return writeCert(t, append(goodCert(), pem.EncodeToMemory(&pem.Block{Type: "OTHER", Bytes: []byte("x")})...), 0600)
		},
		"two tokens": func(t *testing.T) string { return writeCert(t, append(goodCert(), goodCert()...), 0600) },
		"not pem":    func(t *testing.T) string { return writeCert(t, []byte("not a certificate"), 0600) },
	}
	for name, path := range cases {
		t.Run(name, func(t *testing.T) {
			if _, e := readCert(path(t)); e == nil {
				t.Fatal("certificate accepted")
			}
		})
	}
}
