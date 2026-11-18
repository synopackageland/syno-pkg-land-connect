package cloudflare

import "testing"

func TestQuickHostnameFromOutputAcceptsOnlyAnExactTryCloudflareURL(t *testing.T) {
	cases := map[string]string{
		"2026-10-12T09:12:08Z INF |  https://rest-texts-considerations-grass.trycloudflare.com                                 |": "rest-texts-considerations-grass.trycloudflare.com",
		"https://abc-1.trycloudflare.com/":                   "abc-1.trycloudflare.com",
		"https://abc.trycloudflare.com.evil.example":         "",
		"https://a.b.trycloudflare.com":                      "",
		"https://user@abc.trycloudflare.com":                 "",
		"https://abc.trycloudflare.com:8443":                 "",
		"https://abc.trycloudflare.com/path":                 "",
		"https://abc.trycloudflare.com?x=1":                  "",
		"http://abc.trycloudflare.com":                       "",
		"https://-abc.trycloudflare.com":                     "",
		"https://ABC.trycloudflare.com":                      "",
		"https://evil-trycloudflare.com":                     "",
		"Visit https://developers.cloudflare.com/x for help": "",
		"": "",
	}
	for line, want := range cases {
		if got := quickHostnameFromOutput(line); got != want {
			t.Errorf("quickHostnameFromOutput(%q) = %q, want %q", line, got, want)
		}
	}
}

func TestQuickHostnameFromMetricsAcceptsTheOfficialJSONShape(t *testing.T) {
	cases := map[string]string{
		`{"hostname":"rest-texts.trycloudflare.com"}`: "rest-texts.trycloudflare.com",
		`{"hostname":""}`: "",
		`{"hostname":"rest.trycloudflare.com.evil.example"}`: "",
		`{"hostname":"https://rest.trycloudflare.com"}`:      "",
		`{"hostname":"a.trycloudflare.com","extra":true}`:    "",
		`{"hostname":"a.trycloudflare.com"}{"hostname":"b"}`: "",
		`not json`:                             "",
		`{"hostname":["a.trycloudflare.com"]}`: "",
	}
	for body, want := range cases {
		if got := quickHostnameFromMetrics([]byte(body)); got != want {
			t.Errorf("quickHostnameFromMetrics(%q) = %q, want %q", body, got, want)
		}
	}
}

func TestLoginURLAcceptsOnlyTheOfficialSignInPage(t *testing.T) {
	good := "https://dash.cloudflare.com/argotunnel?aud=&callback=https%3A%2F%2Flogin.cloudflareaccess.org%2FAbC123"
	cases := map[string]string{
		good:                 good,
		"   " + good + "   ": good,
		"https://dash.cloudflare.com/argotunnel?aud=&callback=https%3A%2F%2Fevil.example%2F":                     "",
		"https://dash.cloudflare.com/argotunnel?aud=&callback=http%3A%2F%2Flogin.cloudflareaccess.org%2Fx":       "",
		"https://dash.cloudflare.com/argotunnel?aud=":                                                            "",
		"https://dash.cloudflare.com.evil/argotunnel?aud=&callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Fx": "",
		"https://u:p@dash.cloudflare.com/argotunnel?aud=&callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Fx":  "",
		"https://dash.cloudflare.com/other?aud=&callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Fx":           "",
		"http://dash.cloudflare.com/argotunnel?aud=&callback=https%3A%2F%2Flogin.cloudflareaccess.org%2Fx":       "",
		"Please open the following URL and log in with your Cloudflare account:":                                 "",
	}
	for line, want := range cases {
		if got := loginURLFromOutput(line); got != want {
			t.Errorf("loginURLFromOutput(%q) = %q, want %q", line, got, want)
		}
	}
}
