package binaries

import (
	"context"
	"strings"
	"testing"
)

func TestAssetArchitectureFollowsOfficialFileNames(t *testing.T) {
	want := map[string]map[string]string{
		"amd64": {"cloudflared": "amd64", "tailscale": "amd64", "netbird": "amd64"},
		"arm64": {"cloudflared": "arm64", "tailscale": "arm64", "netbird": "arm64"},
		// NetBird publishes no armv7 build; its armv6 build runs on DSM armv7.
		"arm": {"cloudflared": "arm", "tailscale": "arm", "netbird": "armv6"},
	}
	for goarch, products := range want {
		for product, asset := range products {
			got, e := assetArch(product, goarch)
			if e != nil || got != asset {
				t.Errorf("%s on %s = %q, %v; want %q", product, goarch, got, e, asset)
			}
		}
	}
	for _, goarch := range []string{"386", "mips", ""} {
		if _, e := assetArch("cloudflared", goarch); e == nil {
			t.Errorf("unsupported architecture %q accepted", goarch)
		}
	}
	if _, e := assetArch("unknown", "amd64"); e == nil {
		t.Error("unknown product accepted")
	}
}

func TestArmv7OfficialMetadataPicksArmAssets(t *testing.T) {
	s, files, _ := signedFixture(t)
	s.arch = "arm"
	files["/stable/"] = []byte(`{"TarballsVersion":"1.104.1","Tarballs":{"arm":"tailscale_1.104.1_arm.tgz","arm64":"tailscale_1.104.1_arm64.tgz"}}`)
	r, e := s.Latest(context.Background(), "tailscale")
	if e != nil || r.URL != "https://pkgs.tailscale.com/stable/tailscale_1.104.1_arm.tgz" {
		t.Fatal(r, e)
	}
	base := "https://github.com/cloudflare/cloudflared/releases/download/2026.10.0/"
	files["/repos/cloudflare/cloudflared/releases/latest"] = []byte(`{"tag_name":"2026.10.0","assets":[` +
		`{"name":"cloudflared-linux-armhf","browser_download_url":"` + base + `cloudflared-linux-armhf","digest":"sha256:` + strings.Repeat("b", 64) + `"},` +
		`{"name":"cloudflared-linux-arm","browser_download_url":"` + base + `cloudflared-linux-arm","digest":"sha256:1dbe8e4ec17e74bb7f49cf91db6a4903bd0f9fe41984556c7503e40b765fd099"}]}`)
	r, e = s.Latest(context.Background(), "cloudflared")
	if e != nil || r.URL != base+"cloudflared-linux-arm" || r.SHA256 != "1dbe8e4ec17e74bb7f49cf91db6a4903bd0f9fe41984556c7503e40b765fd099" {
		t.Fatal(r, e)
	}
}
