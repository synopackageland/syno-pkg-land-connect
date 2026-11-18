package dsmexternal

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
)

func TestExternalMissingPortsAreUnknownRatherThanZero(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(`{"success":true,"data":{"hostname":""}}`))
	}))
	defer server.Close()
	s, err := ReadExternal(context.Background(), server.URL, "id=fixture", "fixture-token")
	if err != nil {
		t.Fatal(err)
	}
	raw, _ := json.Marshal(s)
	var result map[string]json.RawMessage
	json.Unmarshal(raw, &result)
	for _, field := range []string{"http_port", "https_port"} {
		if string(result[field]) != "null" {
			t.Fatalf("absent %s fabricated: %s", field, raw)
		}
	}
}

func TestExternalGetterOnlyCallsFixedReadAPIAndFiltersResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if q.Get("api") != "SYNO.Core.Web.DSM.External" || q.Get("version") != "1" || q.Get("method") != "get" {
			t.Errorf("wrong API: %v", q)
		}
		if r.Header.Get("Cookie") != "id=fixture" {
			t.Fatal("missing session")
		}
		w.Write([]byte(`{"success":true,"data":{"hostname":"share.example.com","http_port":80,"https_port":443,"secret":"ignored"}}`))
	}))
	defer server.Close()
	s, e := ReadExternal(context.Background(), server.URL, "id=fixture", "fixture-token")
	if e != nil || s.Hostname != "share.example.com" || s.HTTPSPort == nil || *s.HTTPSPort != 443 {
		t.Fatalf("%v %v", s, e)
	}
}

func TestExternalWriterSendsOnlyGivenFieldsWithTheSession(t *testing.T) {
	var form url.Values
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Cookie") != "id=fixture" || r.Header.Get("X-SYNO-TOKEN") != "fixture-token" {
			t.Errorf("request %s %v", r.Method, r.Header)
		}
		r.ParseForm()
		form = r.PostForm
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	host := "nas.example.com"
	if e := WriteExternal(context.Background(), server.URL, "id=fixture", "fixture-token", Update{Hostname: &host, HTTPSPort: port(443)}); e != nil {
		t.Fatal(e)
	}
	if form.Get("api") != "SYNO.Core.Web.DSM.External" || form.Get("method") != "set" || form.Get("version") != "1" || form.Get("hostname") != `"nas.example.com"` || form.Get("https_port") != "443" {
		t.Fatal(form)
	}
	if _, sent := form["http_port"]; sent {
		t.Fatal("unchanged http_port sent", form)
	}
}

// DSM ignores a bare empty hostname but clears the value for the JSON string "".
func TestExternalWriterSendsTheHostnameAsAJSONStringSoEmptyClears(t *testing.T) {
	var got string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r.ParseForm()
		got = r.PostForm.Get("hostname")
		w.Write([]byte(`{"success":true}`))
	}))
	defer server.Close()
	empty := ""
	if e := WriteExternal(context.Background(), server.URL, "id=fixture", "fixture-token", Update{Hostname: &empty}); e != nil || got != `""` {
		t.Fatalf("%v %q", e, got)
	}
}

func TestExternalWriterReportsDSMRefusals(t *testing.T) {
	for _, code := range []int{3510, 3511, 105} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			fmt.Fprintf(w, `{"success":false,"error":{"code":%d}}`, code)
		}))
		host := "nas.example.com"
		e := WriteExternal(context.Background(), server.URL, "id=fixture", "fixture-token", Update{Hostname: &host})
		server.Close()
		var refused *Refused
		if !errors.As(e, &refused) || refused.Code != code {
			t.Fatalf("%d: %v", code, e)
		}
	}
}
