package transport

import (
	"context"
	"io"
	"net/http"
	"strings"
	"testing"
	"time"
)

// recordingDoer records the byte length of each request body it sees and
// returns a preset status per attempt.
type recordingDoer struct {
	statuses  []int
	bodyLens  []int
	callCount int
}

func (d *recordingDoer) Do(req *http.Request) (*http.Response, error) {
	n := 0
	if req.Body != nil {
		b, _ := io.ReadAll(req.Body)
		n = len(b)
	}
	d.bodyLens = append(d.bodyLens, n)
	status := http.StatusOK
	if d.callCount < len(d.statuses) {
		status = d.statuses[d.callCount]
	}
	d.callCount++
	return &http.Response{
		StatusCode: status,
		Body:       io.NopCloser(strings.NewReader("ok")),
		Header:     make(http.Header),
	}, nil
}

// TestRetryReplaysRequestBody guards the retry-with-empty-body bug: a PROPFIND/
// REPORT carries an XML body, and after the first send the body is consumed;
// a retry must resend the full body, not an empty one.
func TestRetryReplaysRequestBody(t *testing.T) {
	body := "<propfind xmlns=\"DAV:\"><prop/></propfind>"
	doer := &recordingDoer{statuses: []int{http.StatusServiceUnavailable, http.StatusMultiStatus}}
	c := New(Options{Doer: doer, MaxRetries: 2, RetryBaseDelay: time.Millisecond, RetryMethods: []string{"PROPFIND"}})

	req, err := http.NewRequest("PROPFIND", "https://caldav.example.com/calendar/", strings.NewReader(body))
	if err != nil {
		t.Fatal(err)
	}
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	defer resp.Body.Close()

	if doer.callCount != 2 {
		t.Fatalf("want 2 attempts (one retry), got %d", doer.callCount)
	}
	if resp.StatusCode != http.StatusMultiStatus {
		t.Fatalf("want final status 207, got %d", resp.StatusCode)
	}
	for attempt, n := range doer.bodyLens {
		if n != len(body) {
			t.Fatalf("attempt %d sent body of %d bytes, want the full %d", attempt, n, len(body))
		}
	}
}

// TestRetryMethodsAreOptIn: a method outside GET/HEAD is sent once unless the
// client lists it in RetryMethods.
func TestRetryMethodsAreOptIn(t *testing.T) {
	doer := &recordingDoer{statuses: []int{http.StatusServiceUnavailable, http.StatusMultiStatus}}
	c := New(Options{Doer: doer, MaxRetries: 2, RetryBaseDelay: time.Millisecond})
	req, _ := http.NewRequest("PROPFIND", "https://caldav.example.com/", strings.NewReader("<x/>"))
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if doer.callCount != 1 {
		t.Fatalf("PROPFIND without RetryMethods: %d attempts, want 1", doer.callCount)
	}
}

// An empty method is a GET, as net/http sends it, so it is retried like one.
func TestEmptyMethodRetriesAsAGet(t *testing.T) {
	doer := &recordingDoer{statuses: []int{http.StatusServiceUnavailable, http.StatusOK}}
	c := New(Options{Doer: doer, MaxRetries: 2, RetryBaseDelay: time.Millisecond})
	req, _ := http.NewRequest(http.MethodGet, "https://jira.example/rest/api/2/myself", nil)
	req.Method = ""
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatalf("Do: %v", err)
	}
	resp.Body.Close()
	if doer.callCount != 2 {
		t.Fatalf("empty method: %d attempts, want 2 (one retry)", doer.callCount)
	}
}

func TestNonReplayableBodyIsNotRetried(t *testing.T) {
	for _, method := range []string{http.MethodGet, "REPORT"} {
		t.Run(method, func(t *testing.T) {
			doer := &recordingDoer{statuses: []int{http.StatusServiceUnavailable, http.StatusOK}}
			c := New(Options{Doer: doer, MaxRetries: 2, RetryBaseDelay: time.Millisecond, RetryMethods: []string{"REPORT"}})
			req, _ := http.NewRequest(method, "https://example.test", io.NopCloser(strings.NewReader("payload")))
			resp, err := c.Do(context.Background(), req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			if doer.callCount != 1 || resp.StatusCode != http.StatusServiceUnavailable {
				t.Fatalf("calls=%d status=%d", doer.callCount, resp.StatusCode)
			}
			body, err := io.ReadAll(resp.Body)
			if err != nil || string(body) != "ok" {
				t.Fatalf("final response body unavailable: %q, %v", body, err)
			}
		})
	}
}

func TestExplicitEmptyBodyCanRetry(t *testing.T) {
	doer := &recordingDoer{statuses: []int{http.StatusServiceUnavailable, http.StatusOK}}
	c := New(Options{Doer: doer, MaxRetries: 1, RetryBaseDelay: time.Millisecond})
	req, _ := http.NewRequest(http.MethodGet, "https://example.test", http.NoBody)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if doer.callCount != 2 {
		t.Fatalf("calls=%d, want 2", doer.callCount)
	}
}

// A POST that AsRead marks is a read, so it is retried like a GET, with its
// body sent again in full. The same POST without the mark is sent once.
func TestAsReadPostIsRetried(t *testing.T) {
	const body = "query=up"
	for _, marked := range []bool{true, false} {
		doer := &recordingDoer{statuses: []int{http.StatusServiceUnavailable, http.StatusOK}}
		c := New(Options{Doer: doer, MaxRetries: 2, RetryBaseDelay: time.Millisecond})
		req, _ := http.NewRequest(http.MethodPost, "https://prometheus.example/api/v1/query", strings.NewReader(body))
		ctx := context.Background()
		if marked {
			ctx = AsRead(ctx)
		}
		resp, err := c.Do(ctx, req)
		if err != nil {
			t.Fatalf("marked %v: %v", marked, err)
		}
		resp.Body.Close()
		want := 1
		if marked {
			want = 2
		}
		if doer.callCount != want {
			t.Fatalf("marked %v: %d attempts, want %d", marked, doer.callCount, want)
		}
		for attempt, n := range doer.bodyLens {
			if n != len(body) {
				t.Fatalf("marked %v: attempt %d sent %d bytes, want %d", marked, attempt, n, len(body))
			}
		}
	}
}

// A read retried after its Doer returned a response with an error does not
// close that response's body again: like *http.Client, the Doer closed it.
func TestRetryDoesNotCloseABodyReturnedWithAnError(t *testing.T) {
	body := &trackedBody{Reader: strings.NewReader("")}
	calls := 0
	c := New(Options{MaxRetries: 1, RetryBaseDelay: time.Millisecond, Doer: doerFunc(func(req *http.Request) (*http.Response, error) {
		calls++
		if calls == 1 {
			return &http.Response{StatusCode: http.StatusFound, Header: http.Header{}, Body: body, Request: req}, io.ErrUnexpectedEOF
		}
		return &http.Response{StatusCode: http.StatusOK, Header: http.Header{}, Body: io.NopCloser(strings.NewReader("ok")), Request: req}, nil
	})})
	req, _ := http.NewRequest(http.MethodGet, "https://example.test/page", nil)
	resp, err := c.Do(context.Background(), req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if calls != 2 || body.closes != 0 {
		t.Fatalf("calls=%d, first body closed %d times; want a retry and no close", calls, body.closes)
	}
}
