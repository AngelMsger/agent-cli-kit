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
