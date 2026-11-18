package cloudflare

import (
	"strings"
	"testing"
)

func TestOutputKeepsOnlyTheFirstFoundValueOfBoundedLines(t *testing.T) {
	l := &lines{find: quickHostnameFromOutput}
	l.Write([]byte(strings.Repeat("x", maxLine) + " https://hidden.trycloudflare.com\n"))
	if got := l.found(); got != "" {
		t.Fatalf("found %q in an over-long line", got)
	}
	l.Write([]byte("INF https://first.trycloudflare.com\nINF https://second.trycloudflare.com\n"))
	if got := l.found(); got != "first.trycloudflare.com" {
		t.Fatalf("found %q", got)
	}
}
