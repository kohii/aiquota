package httpx

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
)

func TestGet_OK(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Test") != "yes" {
			t.Errorf("custom header not forwarded")
		}
		w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()

	body, err := New().Get(context.Background(), srv.URL, map[string]string{"X-Test": "yes"})
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != `{"ok":true}` {
		t.Errorf("body = %q", body)
	}
}

func TestGet_429RetriesOnce(t *testing.T) {
	var calls int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if atomic.AddInt32(&calls, 1) == 1 {
			w.Header().Set("Retry-After", "0")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		w.Write([]byte("ok"))
	}))
	defer srv.Close()

	body, err := New().Get(context.Background(), srv.URL, nil)
	if err != nil {
		t.Fatalf("Get: %v", err)
	}
	if string(body) != "ok" {
		t.Errorf("body = %q", body)
	}
	if got := atomic.LoadInt32(&calls); got != 2 {
		t.Errorf("calls = %d, want 2 (one retry)", got)
	}
}

func TestGet_UnauthorizedStatusError(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		w.Write([]byte(`{"error":"nope"}`))
	}))
	defer srv.Close()

	_, err := New().Get(context.Background(), srv.URL, nil)
	var se *StatusError
	if !errors.As(err, &se) {
		t.Fatalf("expected *StatusError, got %v", err)
	}
	if !se.Unauthorized() {
		t.Errorf("Unauthorized() = false for 401")
	}
}
