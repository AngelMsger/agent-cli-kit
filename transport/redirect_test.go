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

// A write is never followed through a redirect. Go would follow a 302 with a
// GET, whose 200 (a login page) could read as the write's acknowledgment, and
// a 307 by sending the write again. The caller gets the redirect itself.
func TestWriteRedirectIsReturnedNotFollowed(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusSeeOther, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, sameOrigin := range []bool{false, true} {
			var writes, landed atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/login" {
					landed.Add(1)
					w.WriteHeader(http.StatusOK)
					return
				}
				writes.Add(1)
				http.Redirect(w, r, "/login", status)
			}))
			c := New(Options{SameOriginRedirects: sameOrigin})
			req, _ := http.NewRequest(http.MethodPost, srv.URL+"/write", nil)
			resp, err := c.Do(context.Background(), req)
			if err != nil {
				t.Fatalf("%d: %v", status, err)
			}
			resp.Body.Close()
			if resp.StatusCode != status || writes.Load() != 1 || landed.Load() != 0 {
				t.Fatalf("%d (same origin %v): got status %d, %d writes, %d landings; want the redirect itself",
					status, sameOrigin, resp.StatusCode, writes.Load(), landed.Load())
			}
			srv.Close()
		}
	}
}

// Reads still follow redirects: GET, HEAD and the read verbs a CLI declares
// in RetryMethods (CalDAV's PROPFIND).
func TestReadRedirectIsFollowed(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/moved" {
			http.Redirect(w, r, "/here", http.StatusTemporaryRedirect)
			return
		}
		w.WriteHeader(http.StatusOK)
	}))
	defer srv.Close()
	c := New(Options{SameOriginRedirects: true, RetryMethods: []string{"PROPFIND"}})
	for _, method := range []string{http.MethodGet, http.MethodHead, "PROPFIND"} {
		req, _ := http.NewRequest(method, srv.URL+"/moved", nil)
		resp, err := c.Do(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", method, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/here" {
			t.Fatalf("%s: got %d at %s; want the redirect followed", method, resp.StatusCode, resp.Request.URL.Path)
		}
	}
}
