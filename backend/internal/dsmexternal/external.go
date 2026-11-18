// Package dsmexternal reads and changes DSM's external access address for
// Connect, always with the calling administrator's own DSM session.
package dsmexternal

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

type External struct {
	Hostname  string `json:"hostname"`
	HTTPPort  *int   `json:"http_port"`
	HTTPSPort *int   `json:"https_port"`
}

// ReadExternal forwards a freshly authenticated admin request's session only to
// the fixed DSM loopback origin. It stores no credential.
func ReadExternal(ctx context.Context, origin, cookie, token string) (External, error) {
	q := url.Values{"api": {"SYNO.Core.Web.DSM.External"}, "version": {"1"}, "method": {"get"}, "SynoToken": {token}}
	req, e := http.NewRequestWithContext(ctx, "GET", origin+"/webapi/entry.cgi?"+q.Encode(), nil)
	if e != nil {
		return External{}, errors.New("DSM read unavailable")
	}
	req.Header.Set("Cookie", cookie)
	req.Header.Set("X-SYNO-TOKEN", token)
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, e := client.Do(req)
	if e != nil {
		return External{}, errors.New("DSM read unavailable")
	}
	defer res.Body.Close()
	var out struct {
		Success bool     `json:"success"`
		Data    External `json:"data"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&out) != nil || !out.Success {
		return External{}, errors.New("DSM read unavailable")
	}
	return out.Data, nil
}

// Update names the fields to change; nil fields are left as DSM has them.
type Update struct {
	Hostname  *string
	HTTPPort  *int
	HTTPSPort *int
}

// Refused is DSM's own refusal: 3510 invalid value, 3511 equal HTTP and HTTPS
// ports, 105 no permission.
type Refused struct{ Code int }

func (e *Refused) Error() string { return "DSM refused the change: " + strconv.Itoa(e.Code) }

// WriteExternal changes only the address DSM advertises for external links
// (Control Panel > External Access > Advanced), never a port DSM listens on.
// Like ReadExternal it acts with the caller's session, so DSM itself decides.
func WriteExternal(ctx context.Context, origin, cookie, token string, u Update) error {
	form := url.Values{"api": {"SYNO.Core.Web.DSM.External"}, "version": {"1"}, "method": {"set"}}
	if u.Hostname != nil {
		// DSM decodes parameters as JSON: a bare empty value is ignored, while
		// the JSON string "" clears the address.
		quoted, _ := json.Marshal(*u.Hostname)
		form.Set("hostname", string(quoted))
	}
	if u.HTTPPort != nil {
		form.Set("http_port", strconv.Itoa(*u.HTTPPort))
	}
	if u.HTTPSPort != nil {
		form.Set("https_port", strconv.Itoa(*u.HTTPSPort))
	}
	req, e := http.NewRequestWithContext(ctx, "POST", origin+"/webapi/entry.cgi?"+url.Values{"SynoToken": {token}}.Encode(), strings.NewReader(form.Encode()))
	if e != nil {
		return errors.New("DSM write unavailable")
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req.Header.Set("Cookie", cookie)
	req.Header.Set("X-SYNO-TOKEN", token)
	client := http.Client{CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("redirect refused") }}
	res, e := client.Do(req)
	if e != nil {
		return errors.New("DSM write unavailable")
	}
	defer res.Body.Close()
	var out struct {
		Success bool `json:"success"`
		Error   struct {
			Code int `json:"code"`
		} `json:"error"`
	}
	if json.NewDecoder(io.LimitReader(res.Body, 65536)).Decode(&out) != nil {
		return errors.New("DSM write unavailable")
	}
	if !out.Success {
		return &Refused{Code: out.Error.Code}
	}
	return nil
}
