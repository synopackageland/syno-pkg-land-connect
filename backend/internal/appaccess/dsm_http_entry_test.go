package appaccess

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"sync/atomic"
	"testing"

	"dsmapp/internal/apps"
	"dsmapp/internal/connector"
	"dsmapp/internal/ports"
)

func TestDSMTrustUsesDetectedLoopbackTLSIndependentOfAdminEntry(t *testing.T) {
	for _, entry := range []struct{ scheme, host, port string }{{"http", "192.0.2.10", "5000"}, {"https", "192.0.2.10", "5001"}, {"http", "admin.example", "6100"}, {"https", "admin.example", "6443"}} {
		t.Run(entry.scheme+"-"+entry.host+"-"+entry.port, func(t *testing.T) {
			t.Setenv("HTTPS", map[string]string{"http": "off", "https": "on"}[entry.scheme])
			t.Setenv("HTTP_HOST", net.JoinHostPort(entry.host, entry.port))
			t.Setenv("SERVER_PORT", entry.port)
			ctx := context.Background()
			f := newFixture(t, false)
			path := filepath.Join(t.TempDir(), "DSM.json")
			if err := os.WriteFile(path, []byte(fmt.Sprintf(`{"version":1,"port":6100,"ssl":{"port":%d,"redirect":true},"fqdn":"advertised.example"}`, f.dsm.port)), 0600); err != nil {
				t.Fatal(err)
			}
			f.owner.cfg.DSMPort = func(ctx context.Context) (int, error) {
				p, err := (ports.DatastoreDetector{Path: path}).Detect(ctx)
				return p.HTTPS, err
			}
			// A redirect response must never choose a different certificate: inspection
			// only handshakes TLS, it neither GETs nor follows the DSM login redirect.
			var requests atomic.Int64
			f.dsm.server.Config.Handler = http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Redirect(w, r, "https://elsewhere.example/", http.StatusFound)
			})
			review, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public})
			if err != nil || review.DSM == nil || review.DSM.Port != f.dsm.port || review.DSM.Pin.CertSHA256 == "" {
				t.Fatal("browser origin selected the certificate", review, err)
			}
			if _, err = f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: review.DSM, Token: review.Token}); err != nil {
				t.Fatal(err)
			}
			state, err := f.store.Load()
			if err != nil || state.DSM == nil || state.DSM.Port != f.dsm.port || state.DSM.Pin.CertSHA256 != review.DSM.Pin.CertSHA256 {
				t.Fatal("wrong endpoint trusted", state.DSM, err)
			}
			if requests.Load() != 0 {
				t.Fatal("certificate flow followed a login redirect", requests.Load())
			}
		})
	}
}

func TestDSMHTTPSUnavailableHasActionableErrorAndNeverTrustsHTTP(t *testing.T) {
	for _, mode := range []string{"undetected", "disabled-port", "closed", "plain-http"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			f := newFixture(t, false)
			switch mode {
			case "undetected":
				f.owner.cfg.DSMPort = func(context.Context) (int, error) { return 0, ports.ErrDetection }
			case "disabled-port":
				f.owner.cfg.DSMPort = func(context.Context) (int, error) { return 0, nil }
			case "closed":
				f.dsm.server.Close()
			case "plain-http":
				httpOnly := httptest.NewServer(http.RedirectHandler("https://elsewhere.example/", http.StatusMovedPermanently))
				defer httpOnly.Close()
				_, port, _ := net.SplitHostPort(httpOnly.Listener.Addr().String())
				p, _ := strconv.Atoi(port)
				f.owner.cfg.DSMPort = func(context.Context) (int, error) { return p, nil }
			}
			if err := f.owner.TrustDSMFirstUse(ctx); connector.ErrorCode(err) != "apps_dsm_https_unavailable" {
				t.Fatal("no actionable first-use error", err)
			}
			if _, err := f.api.InspectDSM(ctx, apps.DSMInspectRequest{Scope: apps.Public}); apps.ErrorCode(err) != "apps_dsm_https_unavailable" {
				t.Fatal("no actionable review error", err)
			}
			_, err := f.api.SetDSM(ctx, apps.DSMRequest{Scope: apps.Public, DSM: &apps.DSMApproval{Port: 5001}, Token: "fixture"})
			if apps.ErrorCode(err) != "apps_dsm_https_unavailable" {
				t.Fatal("confirmation did not retain HTTPS failure", err)
			}
			state, err := f.store.Load()
			if err != nil || state.DSM != nil {
				t.Fatal("unavailable HTTPS established trust", state.DSM, err)
			}
		})
	}
}

func TestUnavailablePinnedDSMIsNotReportedAsChangedCertificate(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t, false)
	if err := f.owner.TrustDSMFirstUse(ctx); err != nil {
		t.Fatal(err)
	}
	f.dsm.server.Close()
	if err := f.owner.BindTrustedDSM(ctx, apps.Public, "nas.example.com"); connector.ErrorCode(err) != "apps_dsm_https_unavailable" {
		t.Fatal("unreachable TLS was called a certificate change", err)
	}
}
