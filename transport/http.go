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

// Do sends req, applying decorators and retrying transient failures. With a
// nil error, the returned response has a readable, non-nil Body that the
// caller must close. A response returned together with an error (a redirect
// the Doer refused to follow) has its body closed already, as with
// *http.Client; the caller must not close it again. Any returned response has
// a non-nil Request: the request it answers, even when a custom Doer left it
// unset. The context bounds the whole retry sequence.
//
// A request is answered by the last response to a hop this call stands
// behind: for a write, only the request it sent; for a read, any hop that
// kept its method. The default Doer never follows past such a hop. When a
// custom Doer (an *http.Client with Go's default policy, say) followed
// further anyway, Do returns the redirect that left those hops, with an
// empty body, instead of the page it led to: a write's first redirect, or
// the redirect that turned a read with a body (a POST marked with AsRead, a
// method in RetryMethods) into a GET without it. When the Doer stopped
// following with an error and returned its last redirect (its CheckRedirect
// refused a hop, or Go's 10-hop limit), Do returns that answer with the
// error. The write rule in errors then reports a write's unknown outcome, as
// it does with the default Doer: the page at the end answered a different
// request, and on a 307 or 308 the Doer has already sent the write a second
// time. A read gets the redirect, a non-2xx answer, instead of a page that
// answered another request.
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)
	// A request reissued from an earlier redirect chain still names the
	// redirect that created it. That history is not this call's, so the copy
	// sent here starts without it, and answer sees only this call's hops.
	req.Response = nil
	for _, d := range c.decorators {
		d(req)
	}
	if c.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	resp, err := c.doWithRetry(ctx, req)
	if resp != nil {
		resp = answer(resp, err, c.standsBehind(req))
	}
	if resp != nil && resp.Request == nil {
		// A custom Doer may build a response without the request it answers;
		// callers read the request's method and URL from it. A first redirect
		// answered req itself, so this also holds for one answer chose.
		resp.Request = req
	}
	return resp, err
}

// standsBehind reports which hops of req's redirect chain a response may
// answer req from. A write stands behind only the request Do sent, the one
// hop without an earlier redirect. A read stands behind every hop that kept
// its method: a 307 or 308 sends the same read again, while a GET that Go
// sends for a 301, 302 or 303 drops a read's body and is another request.
func (c *Client) standsBehind(req *http.Request) func(*http.Request) bool {
	if !c.isRead(req) {
		return func(hop *http.Request) bool { return hop.Response == nil }
	}
	m := method(req)
	return func(hop *http.Request) bool { return method(hop) == m }
}

// answer returns the last response in resp's redirect chain to a hop that
// stands behind the request (see standsBehind), or resp itself when its own
// request does. net/http links each followed hop's request to the redirect
// that caused it. When a later hop answered, the page at the end is closed
// unread, since it can be large or never end, and the redirect chosen gets
// an empty body (net/http closed its own when it followed). A Doer that
// stopped following with an error (its CheckRedirect refused a hop, or it hit
// its hop limit) returns its last redirect with that error and its body
// closed; the answer replaces it, with an empty body, and the error stays. A
// response that is not a redirect, returned with an error, is left as it is.
func answer(resp *http.Response, err error, stands func(*http.Request) bool) *http.Response {
	redirect := resp.StatusCode >= 300 && resp.StatusCode < 400
	if err != nil && !redirect {
		return resp
	}
	answered := resp
	for answered.Request != nil && !stands(answered.Request) && answered.Request.Response != nil {
		answered = answered.Request.Response
	}
	if err == nil {
		if answered == resp {
			return resp
		}
		if resp.Body != nil {
			_ = resp.Body.Close()
		}
	}
	// With an error the Doer has closed the redirect's body already (see
	// Doer), so it reads as empty instead of failing.
	answered.Body = http.NoBody
	return answered
}
