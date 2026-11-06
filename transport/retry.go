package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// doWithRetry runs req, retrying transient failures for a read: GET, HEAD,
// any Options.RetryMethods, or a request marked with AsRead. Nothing else is
// retried, so a write is sent at most once.
// A transient HTTP status that exhausts retries is returned to the caller as a
// normal response for status classification.
//
// Each attempt is classified by its answer (see answer), not by the page a
// custom Doer's redirects led to: a 503 at the end of a GET that Go sent for
// a read's 302 is not the read's failure, so the read is not sent again and
// that page's Retry-After is not waited for.
//
// Requests with a body (e.g. CalDAV REPORT) can only be retried if the
// body is replayable: the first Do consumes and closes req.Body, so before each
// subsequent attempt we reset it from req.GetBody. http.NewRequest populates
// GetBody for the in-memory body types this package uses (strings/bytes
// readers), so retries send the full body, not an empty one.
func (c *Client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	idempotent := c.isRead(req)
	replayable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil
	stands := c.standsBehind(req)

	for attempt := 0; ; attempt++ {
		if attempt > 0 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
		}
		resp, err := c.doer.Do(req)
		if resp != nil {
			resp = answer(resp, err, stands)
			if errors.Is(err, errRedirectPolicy) {
				// The default Doer refused to follow this redirect: it leaves
				// the origin, or the chain reached 10 hops. Like a redirect it
				// does not follow because the next hop would be another
				// request, it is the request's answer, and the same request
				// is redirected again, so it is not retried.
				err = nil
			}
		}
		if err == nil && !isTransientStatus(resp.StatusCode) {
			return resp, nil
		}
		if !idempotent || !replayable || attempt >= c.maxRetries {
			return resp, err
		}

		delay := c.backoff(attempt, resp)
		if resp != nil && err == nil {
			// A response that came with an error is closed already (see Doer).
			drainAndClose(resp.Body)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(delay):
		}
	}
}

// backoff computes the wait before the next attempt. A Retry-After header (on
// 429/503) takes precedence over linear backoff.
func (c *Client) backoff(attempt int, resp *http.Response) time.Duration {
	if resp != nil {
		if ra := resp.Header.Get("Retry-After"); ra != "" {
			if secs, err := strconv.Atoi(ra); err == nil && secs >= 0 {
				return time.Duration(secs) * time.Second
			}
		}
	}
	return time.Duration(attempt+1) * c.baseDelay
}

func isTransientStatus(status int) bool {
	return status == http.StatusTooManyRequests || status >= 500
}

func drainAndClose(body io.ReadCloser) {
	if body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, body)
	_ = body.Close()
}
