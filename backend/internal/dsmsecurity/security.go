// Package dsmsecurity reads whether two DSM protections are on, with the
// calling user's own DSM session. It changes nothing; a user DSM does not
// let read them gets "unknown", not an error.
package dsmsecurity

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
)

const (
	On      = "on"
	Off     = "off"
	Unknown = "unknown"
)

// Status keys match the catalog's protections, so each one has its link.
type Status struct {
	AutoBlock string `json:"auto-block"`
	TwoFactor string `json:"dsm-2fa"`
}

// Read asks DSM for Control Panel > Security > Protection > Auto Block and
// for 2-factor enforcement. Both need an administrator; others get 105.
func Read(ctx context.Context, origin, cookie, token string) Status {
	s := Status{AutoBlock: Unknown, TwoFactor: Unknown}
	var block struct {
		Enable *bool `json:"enable"`
	}
	if get(ctx, origin, cookie, token, "SYNO.Core.Security.AutoBlock", &block) == nil && block.Enable != nil {
		s.AutoBlock = onOff(*block.Enable)
	}
	var otp struct {
		Option string `json:"otp_enforce_option"`
	}
	if get(ctx, origin, cookie, token, "SYNO.Core.OTP.EnforcePolicy", &otp) == nil {
		// DSM's own choices: nobody, administrators, everyone, chosen users.
		switch otp.Option {
		case "none":
			s.TwoFactor = Off
		case "admin", "user", "custom":
			s.TwoFactor = On
		}
	}
	return s
}

func onOff(on bool) string {
	if on {
		return On
	}
	return Off
}

func get(ctx context.Context, origin, cookie, token, api string, data any) error {
	q := url.Values{"api": {api}, "version": {"1"}, "method": {"get"}, "SynoToken": {token}}
	req, e := http.NewRequestWithContext(ctx, "GET", origin+"/webapi/entry.cgi?"+q.Encode(), nil)
	if e != nil {
		return e
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("X-SYNO-TOKEN", token)
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, e := client.Do(req)
	if e != nil {
		return e
	}
	defer res.Body.Close()
	var out struct {
		Success bool            `json:"success"`
		Data    json.RawMessage `json:"data"`
	}
	if e := json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&out); e != nil {
		return e
	}
	if !out.Success {
		return errors.New("DSM refused the read")
	}
	return json.Unmarshal(out.Data, data)
}
