package transport

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

// DefaultTimeout is the per-request timeout when Options.Timeout is zero.
const DefaultTimeout = 30 * time.Second

// Options configures a Client.
type Options struct {
	// Doer executes requests. Defaults to an *http.Client with Timeout. A
	// custom Doer keeps its own redirect policy for reads, but a write is
	// still answered by its first response: see Client.Do.
	Doer Doer
	// Timeout is the per-request timeout when Doer is nil.
	Timeout time.Duration
	// MaxRetries is the number of additional attempts after the first failure.
	MaxRetries int
	// RetryBaseDelay is the base backoff delay; the Nth retry waits N*base.
	RetryBaseDelay time.Duration
	// Decorators are applied to every request in order.
	Decorators []Decorator
	// Verbose, when non-nil, receives a one-line log of every request's method,
	// URL and status (used by --verbose). Headers and bodies are never logged.
	Verbose io.Writer
	// UserAgent is set on requests that carry none, e.g. "example-cli/1.2.3".
	UserAgent string
	// SameOriginRedirects refuses any redirect that leaves the first request's
	// scheme, host and port; see sameOriginRedirect. It applies only to the
	// default Doer, which never follows a redirect of a write: see
	// redirectPolicy.
	SameOriginRedirects bool
	// RetryMethods adds request methods that are safe to retry (for example the
	// read-only CalDAV verbs PROPFIND and REPORT). GET and HEAD always are. A
	// single request that only reads, whatever its method, is marked with
	// AsRead instead.
	RetryMethods []string
}

// Client is a retrying HTTP client. It is flavor-agnostic: callers build fully
// formed *http.Request values and Client only adds decorators and retries.
type Client struct {
	doer       Doer
	maxRetries int
	baseDelay  time.Duration
	decorators []Decorator
	userAgent  string
	retryable  map[string]bool
}

// New builds a Client from Options, filling defaults.
func New(opt Options) *Client {
	c := &Client{
		doer:       opt.Doer,
		maxRetries: opt.MaxRetries,
		baseDelay:  opt.RetryBaseDelay,
		decorators: opt.Decorators,
		userAgent:  opt.UserAgent,
		retryable:  map[string]bool{http.MethodGet: true, http.MethodHead: true},
	}
	for _, m := range opt.RetryMethods {
		c.retryable[strings.ToUpper(m)] = true
	}
	if c.doer == nil {
		timeout := opt.Timeout
		if timeout == 0 {
			timeout = DefaultTimeout
		}
		hc := &http.Client{Timeout: timeout}
		hc.CheckRedirect = c.redirectPolicy(opt.SameOriginRedirects)
		c.doer = hc
	}
	if opt.Verbose != nil {
		c.doer = &loggingDoer{inner: c.doer, w: opt.Verbose}
	}
	if c.baseDelay == 0 {
		c.baseDelay = 500 * time.Millisecond
	}
	return c
}

var errRedirectPolicy = errors.New("redirect rejected")

// redirectPolicy is the default Doer's CheckRedirect. Only a read is
// followed (see isRead), and only when the next hop sends the same request.
//
// A write is never followed. Go follows a 301, 302 or 303 with a GET, so the
// response would answer that GET (often a login page), not the write; and it
// follows a 307 or 308 by sending the write again to wherever the redirect
// points. Either way a 2xx at the end could read as an acknowledged write
// that never happened. Returning the redirect itself lets the caller see a
// non-2xx answer: under the errors package's write rule its outcome is
// unknown, with the redirect's status.
//
// A read other than a GET or HEAD, such as a posted query or a PROPFIND, is
// followed on a 307 or 308, which Go sends again with the same method and
// body. On a 301, 302 or 303 Go would send a GET without the body, a
// different request whose answer is not the read's, so the redirect is
// returned instead.
func (c *Client) redirectPolicy(sameOrigin bool) func(*http.Request, []*http.Request) error {
	return func(req *http.Request, via []*http.Request) error {
		if len(via) > 0 && (!c.isRead(via[0]) || method(req) != method(via[0])) {
			return http.ErrUseLastResponse
		}
		if sameOrigin {
			return sameOriginRedirect(req, via)
		}
		if len(via) >= 10 {
			return fmt.Errorf("%w: stopped after 10 redirects", errRedirectPolicy)
		}
		return nil
	}
}

type readKey struct{}

// AsRead marks the requests sent with the returned context as reads, whatever
// their method: they are retried on a transient failure and followed through
// a redirect as a GET is. Use it for a request that only reads but cannot be
// a GET, such as a search or a query posted because its parameters do not
// fit in a URL. Pass the marked context to Client.Do, which gives the request
// that context. Never mark a request that can change state: a retry or a
// followed 307 or 308 sends it again.
func AsRead(ctx context.Context) context.Context {
	return context.WithValue(ctx, readKey{}, true)
}

// isRead reports whether req is a read: a GET, a HEAD, a method in
// RetryMethods, or a request whose context AsRead marked. Only a read is
// retried or followed through a redirect.
func (c *Client) isRead(req *http.Request) bool {
	if marked, _ := req.Context().Value(readKey{}).(bool); marked {
		return true
	}
	return c.retryable[method(req)]
}

// method returns req's method in upper case. An empty method is a GET, as
// net/http sends it.
func method(req *http.Request) string {
	if req.Method == "" {
		return http.MethodGet
	}
	return strings.ToUpper(req.Method)
}

// sameOriginRedirect refuses any redirect that leaves the first hop's origin.
// The default http.Client would follow redirects and, because Go's own
// same-domain check ignores scheme and port (and permits subdomains), could
// carry the Authorization header to an HTTP-downgraded, different-port, or
// subdomain target. Requiring an exact scheme+host(+port) match on every hop
// keeps credentials on the configured server. req.URL.Host includes the
// port, so a port change is caught too.
func sameOriginRedirect(req *http.Request, via []*http.Request) error {
	if len(via) >= 10 {
		return fmt.Errorf("%w: stopped after 10 redirects", errRedirectPolicy)
	}
	if len(via) == 0 {
		return nil
	}
	origin := via[0].URL
	if req.URL.Scheme != origin.Scheme || !strings.EqualFold(req.URL.Host, origin.Host) {
		return fmt.Errorf("%w: refusing cross-origin redirect from %s://%s to %s://%s",
			errRedirectPolicy, origin.Scheme, origin.Host, req.URL.Scheme, req.URL.Host)
	}
	return nil
}

// Do sends req, applying decorators and retrying transient failures. The
// returned response (if any) has a readable, non-nil Body that the caller must
// close. The context bounds the whole retry sequence.
//
// A write is answered by its first response. The default Doer never follows
// a write's redirect; when a custom Doer (an *http.Client with Go's default
// policy, say) followed one anyway, Do returns the redirect the chain began
// with, with an empty body, instead of the page it led to. The write rule in
// errors then reports an unknown outcome, as it does with the default Doer:
// the page at the end answered a different request, and on a 307 or 308 the
// Doer has already sent the write a second time.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)
	for _, d := range c.decorators {
		d(req)
	}
	if c.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.doWithRetry(ctx, req)
	if err == nil && !c.isRead(req) {
		resp = firstAnswer(resp)
	}
	return resp, err
}

// firstAnswer returns the redirect a followed chain of responses began with,
// or resp itself when it answered the request directly. net/http links each
// followed hop's request to the redirect that caused it.
func firstAnswer(resp *http.Response) *http.Response {
	if resp == nil || resp.Request == nil || resp.Request.Response == nil {
		return resp
	}
	first := resp.Request.Response
	for first.Request != nil && first.Request.Response != nil {
		first = first.Request.Response
	}
	drainAndClose(resp.Body)
	first.Body = http.NoBody
	return first
}
