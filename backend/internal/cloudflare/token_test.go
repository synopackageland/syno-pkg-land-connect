package cloudflare

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
)

func tokenWithSecret(n int) string {
	raw, _ := json.Marshal(map[string]string{"a": strings.Repeat("a", 32), "t": "12345678-1234-1234-1234-123456789abc", "s": base64.StdEncoding.EncodeToString(make([]byte, n))})
	return base64.StdEncoding.EncodeToString(raw)
}

func TestTokenAcceptsOfficialSecretLengthsWithinABoundedEnvelope(t *testing.T) {
	for _, n := range []int{0, 31, 32, 33, 64, 128, 2048, 4096} {
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			tok := tokenWithSecret(n)
			valid := n >= 32 && len(tok) <= 4096
			if e := validateToken(tok); (e == nil) != valid {
				t.Fatalf("secret of %d bytes: %v", n, e)
			}
		})
	}
	for _, tok := range []string{"", "not base64", base64.StdEncoding.EncodeToString([]byte(`{"a":"x"}`))} {
		if validateToken(tok) == nil {
			t.Errorf("accepted %q", tok)
		}
	}
}
