package main

import (
	"dsmapp/internal/control"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func TestRestoreAnswersReadsAndHoldsMutations(t *testing.T) {
	restored := make(chan struct{})
	h := control.UntilRestored(restored, http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(http.StatusNoContent) }))
	read := httptest.NewRecorder()
	h.ServeHTTP(read, httptest.NewRequest(http.MethodPost, "/apps/list", nil))
	if read.Code != http.StatusNoContent {
		t.Fatal("apps.list must answer while providers restore")
	}
	held := make(chan int, 1)
	go func() {
		w := httptest.NewRecorder()
		h.ServeHTTP(w, httptest.NewRequest(http.MethodPost, "/connect", nil))
		held <- w.Code
	}()
	select {
	case <-held:
		t.Fatal("a connect must wait for restore")
	case <-time.After(20 * time.Millisecond):
	}
	close(restored)
	if code := <-held; code != http.StatusNoContent {
		t.Fatal(code)
	}
}
