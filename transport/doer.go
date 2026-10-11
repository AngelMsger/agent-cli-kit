// Package transport provides a flavor-agnostic HTTP layer: a thin client that
// applies request decorators (auth, user-agent) and retries transient failures.
//
// Every CLI in the family relies on its retry, timeout and decorator behavior;
// change it additively and keep the defaults stable rather than tuning them for
// a single call site.
package transport

import (
	"fmt"
	"io"
	"net/http"
)

// Doer executes an HTTP request. *http.Client satisfies it; tests inject fakes.
type Doer interface {
	Do(*http.Request) (*http.Response, error)
}

// Decorator mutates an outgoing request before it is sent (e.g. adds headers).
type Decorator func(*http.Request)

// loggingDoer wraps a Doer to log each request's method, URL and resulting
// status (or error) to w — used by --verbose. It logs only the request line and
// outcome, never headers or bodies, so credentials are never written out.
type loggingDoer struct {
	inner Doer
	w     io.Writer
}

func (l *loggingDoer) Do(req *http.Request) (*http.Response, error) {
	resp, err := l.inner.Do(req)
	switch {
	case err != nil:
		fmt.Fprintf(l.w, "%s %s -> error: %v\n", req.Method, req.URL, err)
	case resp != nil:
		fmt.Fprintf(l.w, "%s %s -> %d\n", req.Method, req.URL, resp.StatusCode)
	}
	return resp, err
}
