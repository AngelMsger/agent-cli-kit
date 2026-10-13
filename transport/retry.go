package transport

import (
	"context"
	"errors"
	"io"
	"net/http"
	"strconv"
	"time"
)

// doWithRetry runs req, retrying transient failures for idempotent methods:
// GET, HEAD and any Options.RetryMethods. Other methods are never retried, so
// a write is sent at most once.
// A transient HTTP status that exhausts retries is returned to the caller as a
// normal response for status classification.
//
// Requests with a body (e.g. CalDAV REPORT) can only be retried if the
// body is replayable: the first Do consumes and closes req.Body, so before each
// subsequent attempt we reset it from req.GetBody. http.NewRequest populates
// GetBody for the in-memory body types this package uses (strings/bytes
// readers), so retries send the full body, not an empty one.
func (c *Client) doWithRetry(ctx context.Context, req *http.Request) (*http.Response, error) {
	idempotent := c.retryable[req.Method]
	replayable := req.Body == nil || req.Body == http.NoBody || req.GetBody != nil

	for attempt := 0; ; attempt++ {
		if attempt > 0 && req.GetBody != nil {
			body, err := req.GetBody()
			if err != nil {
				return nil, err
			}
			req.Body = body
		}
		resp, err := c.doer.Do(req)
		if err == nil && !isTransientStatus(resp.StatusCode) {
			return resp, nil
		}
		if !idempotent || !replayable || errors.Is(err, errRedirectPolicy) || attempt >= c.maxRetries {
			return resp, err
		}

		delay := c.backoff(attempt, resp)
		if resp != nil {
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
