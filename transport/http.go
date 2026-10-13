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
	// Doer executes requests. Defaults to an *http.Client with Timeout.
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
	// default Doer.
	SameOriginRedirects bool
	// RetryMethods adds request methods that are safe to retry (for example the
	// read-only CalDAV verbs PROPFIND and REPORT). GET and HEAD always are.
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
		if opt.SameOriginRedirects {
			hc.CheckRedirect = sameOriginRedirect
		}
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
func (c *Client) Do(ctx context.Context, req *http.Request) (*http.Response, error) {
	req = req.WithContext(ctx)
	for _, d := range c.decorators {
		d(req)
	}
	if c.userAgent != "" && req.Header.Get("User-Agent") == "" {
		req.Header.Set("User-Agent", c.userAgent)
	}
	return c.doWithRetry(ctx, req)
}
