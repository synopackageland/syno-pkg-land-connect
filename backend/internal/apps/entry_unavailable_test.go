package apps

import (
	"net/http/httptest"
	"strings"
	"testing"
)

func TestChangedEntryPageOffersLocalizedRecoveryWithoutPrivateAddress(t *testing.T) {
	for _, locale := range []string{"en", "zh-TW", "zh-CN", "ja", "ko", "de", "fr", "es", "it"} {
		r := httptest.NewRequest("GET", "https://music.example.com/old?assertion=private", nil)
		r.Header.Set("Accept-Language", locale)
		w := httptest.NewRecorder()
		writeEntryUnavailable(w, r, "/music/", "entry_changed")
		s := w.Body.String()
		if w.Code != 503 || !strings.Contains(s, "Syno Connect Center") || !strings.Contains(s, `action="/music/"`) || strings.Contains(s, "assertion") || strings.Contains(s, "127.0.0.1") {
			t.Fatal(locale, w.Code, s)
		}
	}
}
