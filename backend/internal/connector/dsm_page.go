package connector

import (
	"context"
	"io"
	"net/http"
	"strings"
)

// CheckDSMPage complements API discovery with the actual web entry. It never
// sends a DSM cookie, accepts an external redirect or disables TLS validation.
func CheckDSMPage(ctx context.Context, client *http.Client, address string) error {
	req, e := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(address, "/")+"/", nil)
	if e != nil {
		return Coded("apply_readback_failed", "DSM web entry is unavailable")
	}
	req.Header.Set("Cache-Control", "no-cache, no-store")
	copy := *client
	copy.Jar = nil
	copy.CheckRedirect = func(next *http.Request, via []*http.Request) error {
		if len(via) > 3 || next.URL.Scheme != via[0].URL.Scheme || next.URL.Host != via[0].URL.Host {
			return http.ErrUseLastResponse
		}
		return nil
	}
	res, e := copy.Do(req)
	if e != nil {
		return Coded("apply_readback_failed", "DSM web entry is unavailable")
	}
	defer res.Body.Close()
	raw, e := io.ReadAll(io.LimitReader(res.Body, 65537))
	if e != nil || res.StatusCode != 200 || len(raw) == 0 || len(raw) > 65536 {
		return Coded("apply_readback_failed", "DSM web entry is unavailable")
	}
	body := strings.ToLower(string(raw))
	if !strings.Contains(body, "<html") && !strings.Contains(body, "<!doctype html") {
		return Coded("apply_readback_failed", "DSM web entry is not an HTML page")
	}
	return nil
}
