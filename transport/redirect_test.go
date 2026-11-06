package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httptrace"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// TestRedirectCrossOriginRefused guards the credential-leak vector: the client
// must refuse a redirect that leaves the first hop's origin (here a different
// port), so Authorization is never forwarded off the configured server. The
// refused redirect is the answer, with its status and Location, not an error.
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

	var sent atomic.Int64
	c := New(Options{SameOriginRedirects: true, MaxRetries: 2, RetryBaseDelay: time.Millisecond})
	req, _ := http.NewRequest(http.MethodGet, src.URL+"/", nil)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		WroteRequest: func(httptrace.WroteRequestInfo) { sent.Add(1) },
	}))
	resp, err := c.Do(req.Context(), req)
	if err != nil {
		t.Fatalf("a refused redirect is the answer, not an error: %v", err)
	}
	if reached {
		t.Fatal("request must not reach the cross-origin destination")
	}
	if resp.StatusCode != http.StatusFound {
		t.Fatalf("status = %d, want the refused 302", resp.StatusCode)
	}
	loc, lerr := resp.Location()
	if lerr != nil || loc.String() != dest.URL+"/leak" {
		t.Fatalf("Location = %v (%v), want %s/leak", loc, lerr, dest.URL)
	}
	requireRecoveredRedirect(t, "cross-origin", resp)
	if got := sent.Load(); got != 1 {
		t.Fatalf("requests sent = %d, want 1: a refused redirect is not retried", got)
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

// A redirect loop ends at the tenth hop, whose redirect is the answer, and is
// not retried even when transient request retries are enabled.
func TestRedirectLoopIsBoundedWithoutRetry(t *testing.T) {
	var calls atomic.Int64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		http.Redirect(w, r, "/loop", http.StatusFound)
	}))
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodGet, srv.URL+"/loop", nil)
	for _, sameOrigin := range []bool{true, false} {
		calls.Store(0)
		resp, err := New(Options{SameOriginRedirects: sameOrigin, MaxRetries: 3, RetryBaseDelay: time.Millisecond}).Do(context.Background(), req)
		if err != nil {
			t.Fatalf("sameOrigin=%v: the tenth redirect is the answer, not an error: %v", sameOrigin, err)
		}
		if resp.StatusCode != http.StatusFound {
			t.Fatalf("sameOrigin=%v: status = %d, want 302", sameOrigin, resp.StatusCode)
		}
		requireRecoveredRedirect(t, fmt.Sprintf("loop sameOrigin=%v", sameOrigin), resp)
		if got := calls.Load(); got != 10 {
			t.Fatalf("sameOrigin=%v: requests=%d, want 10 including all retries", sameOrigin, got)
		}
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
// in RetryMethods (CalDAV's PROPFIND), and an empty method, which is a GET.
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
	for _, method := range []string{http.MethodGet, http.MethodHead, "PROPFIND", ""} {
		req, _ := http.NewRequest(http.MethodGet, srv.URL+"/moved", nil)
		// net/http sends an empty method as a GET; NewRequest would fill it in.
		req.Method = method
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

// A read other than a GET or HEAD follows a 307 or 308, which sends the same
// method and body again. That covers a POST that AsRead marks (a posted query)
// and a method in RetryMethods. v0.7.0 returned the redirect of any POST, so
// a posted query behind an http-to-https 308 stopped working.
func TestBodyReadFollowsSameMethodRedirect(t *testing.T) {
	const body = "query=up"
	for _, status := range []int{http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		for _, method := range []string{http.MethodPost, "PROPFIND"} {
			var gotMethod, gotBody string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/moved" {
					http.Redirect(w, r, "/here", status)
					return
				}
				b, _ := io.ReadAll(r.Body)
				gotMethod, gotBody = r.Method, string(b)
				w.WriteHeader(http.StatusOK)
			}))
			c := New(Options{SameOriginRedirects: true, RetryMethods: []string{"PROPFIND"}})
			ctx := context.Background()
			if method == http.MethodPost {
				ctx = AsRead(ctx) // PROPFIND is a read through RetryMethods
			}
			req, _ := http.NewRequest(method, srv.URL+"/moved", strings.NewReader(body))
			resp, err := c.Do(ctx, req)
			if err != nil {
				t.Fatalf("%s %d: %v", method, status, err)
			}
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK || gotMethod != method || gotBody != body {
				t.Fatalf("%s %d: got %d, %s with %q at the target; want the same request followed",
					method, status, resp.StatusCode, gotMethod, gotBody)
			}
			srv.Close()
		}
	}
}

// On a 301, 302 or 303 Go would follow a read with a body as a GET without
// that body: a different request, whose answer is not the read's. The read
// gets the redirect itself instead. A PROPFIND used to be followed this way.
func TestBodyReadIsNotTurnedIntoAGet(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther} {
		for _, method := range []string{http.MethodPost, "PROPFIND"} {
			var landed atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/here" {
					landed.Add(1)
					w.WriteHeader(http.StatusOK)
					return
				}
				http.Redirect(w, r, "/here", status)
			}))
			c := New(Options{RetryMethods: []string{"PROPFIND"}})
			req, _ := http.NewRequest(method, srv.URL+"/moved", strings.NewReader("query=up"))
			resp, err := c.Do(AsRead(context.Background()), req)
			if err != nil {
				t.Fatalf("%s %d: %v", method, status, err)
			}
			resp.Body.Close()
			if resp.StatusCode != status || landed.Load() != 0 {
				t.Fatalf("%s %d: got %d after %d GETs at the target; want the redirect itself",
					method, status, resp.StatusCode, landed.Load())
			}
			srv.Close()
		}
	}
}

// A custom Doer keeps its own redirect policy, so Go's default client follows
// a write's 302 with a GET and its 307 by sending the write again. The write
// is still answered by its first response: Do returns the redirect, so the
// caller cannot read the login page at the end as an acknowledgment. Before,
// only the default Doer was covered, and a library caller that passed its own
// *http.Client got the login page's 200.
func TestCustomDoerWriteIsAnsweredByItsRedirect(t *testing.T) {
	for _, status := range []int{http.StatusFound, http.StatusTemporaryRedirect} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/login":
				_, _ = w.Write([]byte("<html>sign in</html>"))
			case "/hop":
				http.Redirect(w, r, "/login", status)
			default:
				http.Redirect(w, r, "/hop", status)
			}
		}))
		c := New(Options{Doer: &http.Client{}})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/write", strings.NewReader("x"))
		resp, err := c.Do(context.Background(), req)
		if err != nil {
			t.Fatalf("%d: %v", status, err)
		}
		requireRecoveredRedirect(t, fmt.Sprint(status), resp)
		if resp.StatusCode != status || resp.Request.URL.Path != "/write" {
			t.Fatalf("%d: got %d for %s; want the first redirect", status, resp.StatusCode, resp.Request.URL.Path)
		}

		// A read through the same Doer still ends where its redirects lead.
		req, _ = http.NewRequest(http.MethodGet, srv.URL+"/page", nil)
		resp, err = c.Do(context.Background(), req)
		if err != nil {
			t.Fatalf("%d read: %v", status, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/login" {
			t.Fatalf("%d read: got %d at %s; want the redirect followed", status, resp.StatusCode, resp.Request.URL.Path)
		}
		srv.Close()
	}
}

// A custom Doer that gives up following a write's redirects (its own
// CheckRedirect, or net/http's 10-hop limit) returns its last redirect, with
// the body closed, and an error. The write is still answered by its first
// redirect, and the error stays.
func TestCustomDoerWriteRedirectErrorIsAnsweredByItsRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, fmt.Sprintf("/hop%d", len(r.URL.Path)), http.StatusFound)
	}))
	defer srv.Close()
	stopAtSecond := &http.Client{CheckRedirect: func(_ *http.Request, via []*http.Request) error {
		if len(via) >= 2 {
			return errors.New("policy: no more hops")
		}
		return nil
	}}
	refuseFirst := &http.Client{CheckRedirect: func(*http.Request, []*http.Request) error {
		return errors.New("policy: no redirects")
	}}
	for name, doer := range map[string]*http.Client{"hop limit": {}, "policy at the second hop": stopAtSecond, "policy at the first hop": refuseFirst} {
		c := New(Options{Doer: doer})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/write", strings.NewReader("x"))
		resp, err := c.Do(context.Background(), req)
		if err == nil || resp == nil {
			t.Fatalf("%s: got %v, %v; want the first redirect with the Doer's error", name, resp, err)
		}
		requireRecoveredRedirect(t, name, resp)
		if resp.StatusCode != http.StatusFound || resp.Request.URL.Path != "/write" {
			t.Fatalf("%s: got %d for %s; want the first redirect", name, resp.StatusCode, resp.Request.URL.Path)
		}
	}
}

type doerFunc func(*http.Request) (*http.Response, error)

func (f doerFunc) Do(req *http.Request) (*http.Response, error) { return f(req) }

type trackedBody struct {
	io.Reader
	closes int
}

func (b *trackedBody) Close() error { b.closes++; return nil }

// A custom Doer may answer a write with any response and an error. The error
// stays, and a 3xx is still the write's answer with an empty body. As with
// *http.Client, a body returned with an error is the Doer's to have closed,
// so Do never closes it again: a second Close is not safe for every body.
func TestCustomDoerWriteKeepsItsError(t *testing.T) {
	failure := errors.New("unexpected EOF")
	for _, status := range []int{http.StatusFound, http.StatusOK, http.StatusBadGateway} {
		body := &trackedBody{Reader: strings.NewReader("page")}
		c := New(Options{Doer: doerFunc(func(req *http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: status, Header: http.Header{"Location": {"/login"}}, Body: body, Request: req}, failure
		})})
		req, _ := http.NewRequest(http.MethodPost, "https://example.test/write", strings.NewReader("x"))
		resp, err := c.Do(context.Background(), req)
		if !errors.Is(err, failure) || resp == nil || resp.StatusCode != status {
			t.Fatalf("%d: got %v, %v; want the response with its error", status, resp, err)
		}
		redirect := status >= 300 && status < 400
		if body.closes != 0 || (resp.Body == http.NoBody) != redirect {
			t.Fatalf("%d: body closed %d times, replaced=%v; want it never closed, replaced only for a redirect", status, body.closes, resp.Body == http.NoBody)
		}
	}
}

// The page a followed write redirect led to is closed exactly once, by Do.
func TestCustomDoerWriteClosesTheDiscardedPageOnce(t *testing.T) {
	page := &trackedBody{Reader: strings.NewReader("sign in")}
	c := New(Options{Doer: doerFunc(func(req *http.Request) (*http.Response, error) {
		first := &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/login"}}, Body: http.NoBody, Request: req}
		hop, _ := http.NewRequest(http.MethodGet, "https://example.test/login", nil)
		hop.Response = first
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: page, Request: hop}, nil
	})})
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/write", strings.NewReader("x"))
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || page.closes != 1 {
		t.Fatalf("got %d with the page closed %d times; want the first redirect and one close", resp.StatusCode, page.closes)
	}
}

// A custom Doer may build a response without the request it answers. Do fills
// it in, so a caller can read the request's method and URL from any response.
func TestResponseAlwaysNamesItsRequest(t *testing.T) {
	for _, method := range []string{http.MethodGet, http.MethodPost} {
		c := New(Options{Doer: doerFunc(func(*http.Request) (*http.Response, error) {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/login"}}, Body: io.NopCloser(strings.NewReader(""))}, nil
		})})
		req, _ := http.NewRequest(method, "https://example.test/job/app/7/stop", strings.NewReader("x"))
		resp, err := c.Do(context.Background(), req)
		if err != nil {
			t.Fatal(err)
		}
		resp.Body.Close()
		if resp.Request == nil || resp.Request.Method != method || resp.Request.URL.Path != "/job/app/7/stop" {
			t.Fatalf("%s: response names %+v; want the request it answers", method, resp.Request)
		}
	}
}

// The page a custom Doer's redirect led to is closed unread: a page that never
// ends must not hold up the write's answer.
func TestCustomDoerWriteDoesNotReadTheRedirectTarget(t *testing.T) {
	stop := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/stream" {
			http.Redirect(w, r, "/stream", http.StatusFound)
			return
		}
		chunk := []byte(strings.Repeat("x", 4096))
		for {
			select {
			case <-stop:
				return
			case <-r.Context().Done():
				return
			default:
			}
			if _, err := w.Write(chunk); err != nil {
				return
			}
			w.(http.Flusher).Flush()
		}
	}))
	defer srv.Close()
	defer close(stop) // ends the page first, so a failing Do returns and Close does not wait
	c := New(Options{Doer: &http.Client{}})
	done := make(chan *http.Response, 1)
	go func() {
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/write", strings.NewReader("x"))
		resp, err := c.Do(context.Background(), req)
		if err != nil {
			t.Error(err)
		}
		done <- resp
	}()
	select {
	case resp := <-done:
		if resp != nil {
			resp.Body.Close()
			if resp.StatusCode != http.StatusFound {
				t.Fatalf("got %d, want the first redirect", resp.StatusCode)
			}
		}
	case <-time.After(5 * time.Second):
		t.Fatal("Do read the endless page the write's redirect led to")
	}
}

// A marked POST whose body cannot be replayed is a read that Go cannot send
// again: it is not retried, and a 307 or 308 comes back instead of being
// followed without its body.
func TestNonReplayableMarkedPostIsSentOnce(t *testing.T) {
	for _, status := range []int{http.StatusServiceUnavailable, http.StatusTemporaryRedirect, http.StatusPermanentRedirect} {
		var hits []string
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			hits = append(hits, r.Method+" "+r.URL.Path)
			if r.URL.Path == "/query" {
				if status >= 500 {
					w.WriteHeader(status)
					return
				}
				http.Redirect(w, r, "/elsewhere", status)
			}
		}))
		c := New(Options{MaxRetries: 2, RetryBaseDelay: time.Millisecond})
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/query", io.NopCloser(strings.NewReader("query=up")))
		resp, err := c.Do(AsRead(context.Background()), req)
		if err != nil {
			t.Fatalf("%d: %v", status, err)
		}
		resp.Body.Close()
		srv.Close()
		if resp.StatusCode != status || len(hits) != 1 || hits[0] != "POST /query" {
			t.Fatalf("%d: got %d after %v; want one POST answered with %d", status, resp.StatusCode, hits, status)
		}
	}
}

// A request reissued from an earlier redirect chain still names the redirect
// that created it. That history is not this call's: the write is answered by
// this call's own response, with the default Doer and with a custom one.
func TestStaleRedirectHistoryIsNotThisCallsAnswer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusCreated)
	}))
	defer srv.Close()
	created := doerFunc(func(*http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: http.StatusCreated, Header: http.Header{}, Body: io.NopCloser(strings.NewReader(""))}, nil
	})
	for name, doer := range map[string]Doer{"default": nil, "custom without Request": created} {
		earlier, _ := http.NewRequest(http.MethodPost, srv.URL+"/old", nil)
		req, _ := http.NewRequest(http.MethodPost, srv.URL+"/write", strings.NewReader("x"))
		req.Response = &http.Response{StatusCode: http.StatusFound, Header: http.Header{}, Body: http.NoBody, Request: earlier}
		resp, err := New(Options{Doer: doer}).Do(context.Background(), req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated || resp.Request == nil || resp.Request.URL.Path != "/write" || resp.Request.Response != nil {
			t.Fatalf("%s: got %d for %+v; want this call's 201", name, resp.StatusCode, resp.Request)
		}
	}
}

// A custom Doer may supply a chain whose first redirect does not name its
// request. The first redirect answered the write itself, so Do names it.
func TestRecoveredFirstRedirectNamesItsRequest(t *testing.T) {
	doer := doerFunc(func(req *http.Request) (*http.Response, error) {
		first := &http.Response{StatusCode: http.StatusFound, Header: http.Header{"Location": {"/login"}}, Body: http.NoBody}
		hop, _ := http.NewRequest(http.MethodGet, "https://example.test/login", nil)
		hop.Response = first
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("sign in")), Request: hop}, nil
	})
	req, _ := http.NewRequest(http.MethodPost, "https://example.test/job/app/7/stop", strings.NewReader("x"))
	resp, err := New(Options{Doer: doer}).Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusFound || resp.Request == nil || resp.Request.Method != http.MethodPost || resp.Request.URL.Path != "/job/app/7/stop" {
		t.Fatalf("got %d for %+v; want the first redirect naming the write", resp.StatusCode, resp.Request)
	}
}

// bodyReads are the two ways a request with a body is a read: a POST marked
// with AsRead, and a method in RetryMethods (PROPFIND here) sent unmarked.
var bodyReads = []struct {
	method string
	ctx    func() context.Context
}{
	{http.MethodPost, func() context.Context { return AsRead(context.Background()) }},
	{"PROPFIND", context.Background},
}

// requireRecoveredRedirect fails unless resp's body is http.NoBody, the empty
// body Do gives a redirect it answers with in place of the page it led to,
// and reads as empty without an error.
func requireRecoveredRedirect(t *testing.T, name string, resp *http.Response) {
	t.Helper()
	if resp.Body != http.NoBody {
		t.Fatalf("%s: body is %T; want http.NoBody", name, resp.Body)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil || len(body) != 0 {
		t.Fatalf("%s: read %q (%v); want an empty body", name, body, err)
	}
}

// A custom Doer keeps its own redirect policy, so Go's default client follows
// a body read's 302 with a GET that carries no body: a different request,
// whose 200 (here an empty JSON object) would read as the read's answer. Do
// answers the read with the last response to a request that kept its method,
// as the default Doer does by not following: the 302 here. Before, only a
// write's chain was cut back, so a posted search through a caller's
// *http.Client decoded the GET's page as an empty result.
func TestCustomDoerBodyReadIsNotAnsweredByAGet(t *testing.T) {
	for _, status := range []int{http.StatusMovedPermanently, http.StatusFound, http.StatusSeeOther} {
		for _, read := range bodyReads {
			name := fmt.Sprintf("%s %d", read.method, status)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path == "/page" {
					_, _ = w.Write([]byte("{}"))
					return
				}
				http.Redirect(w, r, "/page", status)
			}))
			c := New(Options{Doer: &http.Client{}, RetryMethods: []string{"PROPFIND"}})
			req, _ := http.NewRequest(read.method, srv.URL+"/search", strings.NewReader("query=up"))
			resp, err := c.Do(read.ctx(), req)
			if err != nil {
				t.Fatalf("%s: %v", name, err)
			}
			requireRecoveredRedirect(t, name, resp)
			if resp.StatusCode != status || resp.Request.Method != read.method || resp.Request.URL.Path != "/search" {
				t.Fatalf("%s: got %d for %s %s; want the redirect that changed the method",
					name, resp.StatusCode, resp.Request.Method, resp.Request.URL.Path)
			}
			srv.Close()
		}
	}
}

// A read may follow a 307 or 308, which sends the same method and body
// again, so through a custom Doer it still ends at the page those hops lead
// to. When a later hop turns it into a GET, the read is answered by the
// redirect that did: the last response to a request with the read's method.
func TestCustomDoerBodyReadKeepsItsSameMethodHops(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/a":
			http.Redirect(w, r, "/b", http.StatusTemporaryRedirect)
		case "/b":
			b, _ := io.ReadAll(r.Body)
			_, _ = w.Write([]byte(r.Method + " " + string(b)))
		case "/c":
			http.Redirect(w, r, "/d", http.StatusTemporaryRedirect)
		case "/d":
			http.Redirect(w, r, "/page", http.StatusFound)
		default:
			_, _ = w.Write([]byte("{}"))
		}
	}))
	defer srv.Close()
	c := New(Options{Doer: &http.Client{}, RetryMethods: []string{"PROPFIND"}})

	for _, read := range bodyReads {
		req, _ := http.NewRequest(read.method, srv.URL+"/a", strings.NewReader("query=up"))
		resp, err := c.Do(read.ctx(), req)
		if err != nil {
			t.Fatalf("%s 307: %v", read.method, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if want := read.method + " query=up"; readErr != nil || resp.StatusCode != http.StatusOK || resp.Request.URL.Path != "/b" || string(body) != want {
			t.Fatalf("%s 307: got %d for %s with %q (%v); want %q, the read followed with its body",
				read.method, resp.StatusCode, resp.Request.URL.Path, body, readErr, want)
		}

		name := read.method + " 307 then 302"
		req, _ = http.NewRequest(read.method, srv.URL+"/c", strings.NewReader("query=up"))
		resp, err = c.Do(read.ctx(), req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		requireRecoveredRedirect(t, name, resp)
		if resp.StatusCode != http.StatusFound || resp.Request.Method != read.method || resp.Request.URL.Path != "/d" {
			t.Fatalf("%s: got %d for %s %s; want the 302 that turned it into a GET",
				name, resp.StatusCode, resp.Request.Method, resp.Request.URL.Path)
		}
	}
}

// A read's retries are decided by its answer, not by the page a custom Doer's
// redirects led to. A 503 at the end of the GET that Go sent for the read's
// 302 is that GET's failure: the read is answered by its 302 at once, not
// sent again after the page's Retry-After. A 503 to a hop that kept the
// read's method is the read's own, and is still retried. Before, Do chose the
// answer only after the retries, so the read waited out the page's
// Retry-After and was sent again.
func TestCustomDoerBodyReadRetriesOnItsOwnAnswer(t *testing.T) {
	for _, read := range bodyReads {
		var hits atomic.Int32
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.URL.Path {
			case "/search":
				hits.Add(1)
				http.Redirect(w, r, "/page", http.StatusFound)
			case "/page":
				w.Header().Set("Retry-After", "30")
				w.WriteHeader(http.StatusServiceUnavailable)
			case "/a":
				hits.Add(1)
				http.Redirect(w, r, "/b", http.StatusTemporaryRedirect)
			case "/b":
				if hits.Load() == 1 {
					w.WriteHeader(http.StatusServiceUnavailable)
					return
				}
				_, _ = w.Write([]byte("ok"))
			}
		}))
		c := New(Options{Doer: &http.Client{}, MaxRetries: 2, RetryBaseDelay: time.Millisecond, RetryMethods: []string{"PROPFIND"}})

		name := read.method + " 302 to a 503"
		ctx, cancel := context.WithTimeout(read.ctx(), 5*time.Second)
		req, _ := http.NewRequest(read.method, srv.URL+"/search", strings.NewReader("query=up"))
		resp, err := c.Do(ctx, req)
		cancel()
		if err != nil {
			t.Fatalf("%s: %v; want the 302 without waiting for a retry", name, err)
		}
		requireRecoveredRedirect(t, name, resp)
		if resp.StatusCode != http.StatusFound || hits.Load() != 1 {
			t.Fatalf("%s: got %d after %d sends; want the 302 after one", name, resp.StatusCode, hits.Load())
		}

		hits.Store(0)
		name = read.method + " 307 to a 503"
		req, _ = http.NewRequest(read.method, srv.URL+"/a", strings.NewReader("query=up"))
		resp, err = c.Do(read.ctx(), req)
		if err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		body, readErr := io.ReadAll(resp.Body)
		resp.Body.Close()
		if readErr != nil || resp.StatusCode != http.StatusOK || string(body) != "ok" || hits.Load() != 2 {
			t.Fatalf("%s: got %d with %q (%v) after %d sends; want the retry's 200 after two",
				name, resp.StatusCode, body, readErr, hits.Load())
		}
		srv.Close()
	}
}
