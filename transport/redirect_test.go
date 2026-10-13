package transport

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// TestRedirectCrossOriginRefused guards the credential-leak vector: the client
// must refuse a redirect that leaves the first hop's origin (here a different
// port), so Authorization is never forwarded off the configured server.
func TestRedirectCrossOriginRefused(t *testing.T) {
	reached := false
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	defer dest.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/leak", http.StatusFound)
	}))
	defer src.Close()

	c := New(Options{SameOriginRedirects: true})
	req, _ := http.NewRequest(http.MethodGet, src.URL+"/", nil)
	resp, err := c.Do(context.Background(), req)
	if resp != nil {
		resp.Body.Close()
	}
	if err == nil {
		t.Fatal("cross-origin redirect should be refused")
	}
	if reached {
		t.Fatal("request must not reach the cross-origin destination")
	}
}

// TestRedirectSameOriginFollowed confirms the guard is not over-broad: a
// same-origin redirect is still followed normally.
func TestRedirectSameOriginFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/final" {
			w.WriteHeader(http.StatusOK)
			return
		}
		http.Redirect(w, r, "/final", http.StatusFound)
	}))
	defer srv.Close()

	c := New(Options{SameOriginRedirects: true})
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/start", nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("same-origin redirect should be followed: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("want 200 after same-origin redirect, got %d", resp.StatusCode)
	}
}

// TestRedirectCrossOriginFollowedByDefault keeps today's behaviour for CLIs that
// have not opted in: Go's own redirect policy applies.
func TestRedirectCrossOriginFollowedByDefault(t *testing.T) {
	dest := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))
	defer dest.Close()
	src := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, dest.URL+"/moved", http.StatusFound)
	}))
	defer src.Close()

	req, _ := http.NewRequest(http.MethodGet, src.URL+"/", nil)
	resp, err := New(Options{}).Do(context.Background(), req)
	if err != nil {
		t.Fatalf("redirect should be followed without SameOriginRedirects: %v", err)
	}
	resp.Body.Close()
}

// Policy failures are terminal even when transient request retries are enabled.
func TestRedirectLoopIsBoundedWithoutRetry(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/loop", nil)
	resp, err := New(Options{SameOriginRedirects: true, MaxRetries: 3, RetryBaseDelay: time.Millisecond}).Do(context.Background(), req)
	if resp != nil {
		resp.Body.Close()
	}
	if !errors.Is(err, errRedirectPolicy) {
		t.Fatalf("want redirect policy error, got %v", err)
	}
	if got := calls.Load(); got != 10 {
		t.Fatalf("requests=%d, want 10 including all retries", got)
	}
}
