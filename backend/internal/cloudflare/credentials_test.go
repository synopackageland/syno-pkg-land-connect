package cloudflare

import (
	"encoding/base64"
	"fmt"
	"strings"
	"testing"
)

func fixtureCredentials() []byte {
	return []byte(`{"AccountTag":"0123456789abcdef0123456789abcdef","TunnelID":"12345678-1234-1234-1234-123456789abc","TunnelSecret":"` + base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32))) + `"}`)
}
func TestCredentialsParseCanonicalPrivateDocumentAndRedactedFormatting(t *testing.T) {
	c, e := parseCredentials(fixtureCredentials())
	if e != nil {
		t.Fatal(e)
	}
	if c.tunnelID != "12345678-1234-1234-1234-123456789abc" {
		t.Fatal("wrong tunnel")
	}
	if strings.Contains(fmt.Sprintf("%v %+v %#v", c, c, c), base64.StdEncoding.EncodeToString([]byte(strings.Repeat("x", 32)))) {
		t.Fatal("format leaked credentials")
	}
	canonical := c.privateJSON()
	if !strings.Contains(string(canonical), "TunnelSecret") {
		t.Fatal("private document missing secret")
	}
}
func TestCredentialsRejectTokensAccountCertificatesEndpointsAndDuplicateFields(t *testing.T) {
	valid := string(fixtureCredentials())
	cases := []string{"", "token-looking-input", "-----BEGIN CERTIFICATE-----", "{}", valid + " {}", strings.Replace(valid, "\"AccountTag\":", "\"AccountTag\":\"00000000000000000000000000000000\",\"AccountTag\":", 1), strings.Replace(valid, "AccountTag", "accounttag", 1), strings.Replace(valid, "12345678-1234-1234-1234-123456789abc", "../credentials", 1), strings.Replace(valid, "eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHh4eHg=", "short", 1), strings.TrimSuffix(valid, "}") + `,"Endpoint":"evil.invalid"}`, strings.TrimSuffix(valid, "}") + `,"token":"SYNTHETIC-SECRET"}`}
	for i, raw := range cases {
		_, e := parseCredentials([]byte(raw))
		if e == nil {
			t.Fatalf("case %d accepted", i)
		}
		if strings.Contains(e.Error(), "SYNTHETIC") || strings.Contains(e.Error(), "eHh4") {
			t.Fatal("error leaked input")
		}
	}
}
