package errors

import (
	"encoding/json"
	"net/url"
	"strings"
	"testing"
)

var testGuide = RedirectGuide{Setting: "--base-url", Steps: []string{"example-cli doctor"}}

func TestWithRedirectToALocation(t *testing.T) {
	t.Parallel()
	loc, _ := url.Parse("https://user:secret@sso.example.com/login/start?token=abc#frag")
	e := New(FromHTTPStatus(302), "HTTP_FOUND", "server returned HTTP 302").
		WithHTTPStatus(302).WithRedirect(loc, testGuide)

	if e.Retryable {
		t.Error("a redirect must not be retryable: the same request is redirected again")
	}
	if !e.IsRedirect() {
		t.Error("IsRedirect() = false, want true for a redirect with a location")
	}
	if e.Category != CategoryServer || e.Code != "HTTP_FOUND" || e.HTTPStatus != 302 {
		t.Errorf("category/code/status changed: %s %s %d", e.Category, e.Code, e.HTTPStatus)
	}
	if !strings.Contains(e.Hint, "redirected the request to https://sso.example.com/login/start:") {
		t.Errorf("hint does not name the target: %q", e.Hint)
	}
	for _, leaked := range []string{"secret", "user", "token", "abc", "frag"} {
		if strings.Contains(e.Hint, leaked) {
			t.Errorf("hint leaks %q: %q", leaked, e.Hint)
		}
	}
	if !strings.Contains(e.Hint, "check --base-url first") {
		t.Errorf("hint does not name the setting: %q", e.Hint)
	}
	if len(e.NextSteps) != 1 || e.NextSteps[0] != "example-cli doctor" {
		t.Errorf("next steps = %v, want the guide's", e.NextSteps)
	}
}

func TestWithRedirectWithoutALocation(t *testing.T) {
	t.Parallel()
	for _, status := range []int{300, 304, 302} {
		e := New(FromHTTPStatus(status), "X", "msg").WithHTTPStatus(status).WithRedirect(nil, testGuide)
		if e.Retryable {
			t.Errorf("%d: retryable, want not: the same request gets the same answer", status)
		}
		if e.IsRedirect() {
			t.Errorf("%d: IsRedirect() = true without a location", status)
		}
		if strings.Contains(e.Hint, "login page") {
			t.Errorf("%d: hint claims a redirect it cannot see: %q", status, e.Hint)
		}
		if !strings.Contains(e.Hint, "HTTP ") || !strings.Contains(e.Hint, "check --base-url first") {
			t.Errorf("%d: hint = %q", status, e.Hint)
		}
	}
}

// A write's redirect keeps the write rule's hint and read steps.
func TestWriteRedirectKeepsTheWriteRule(t *testing.T) {
	t.Parallel()
	loc, _ := url.Parse("https://sso.example.com/login")
	cause := New(FromHTTPStatus(302), "HTTP_FOUND", "server returned HTTP 302").
		WithHTTPStatus(302).WithRedirect(loc, testGuide)
	out := AsCLIError(FromWrite(cause, WriteTarget{Description: "comment", ReadSteps: []string{"example-cli comment list"}}))
	if out.Hint != redirectWriteHint {
		t.Errorf("hint = %q, want the write rule's redirect hint", out.Hint)
	}
	if len(out.NextSteps) != 1 || out.NextSteps[0] != "example-cli comment list" {
		t.Errorf("next steps = %v, want the target's read steps", out.NextSteps)
	}
	if out.Retryable {
		t.Error("a write's redirect must not be retryable")
	}
}

// WithRedirect adds no field to the JSON error envelope.
func TestWithRedirectKeepsTheEnvelope(t *testing.T) {
	t.Parallel()
	loc, _ := url.Parse("https://sso.example.com/login")
	e := New(CategoryServer, "X", "msg").WithHTTPStatus(302).WithRedirect(loc, testGuide)
	raw, err := json.Marshal(e.Payload())
	if err != nil {
		t.Fatal(err)
	}
	var got map[string]map[string]any
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	for key := range got["error"] {
		switch key {
		case "category", "code", "message", "hint", "next_steps", "retryable", "http_status":
		default:
			t.Errorf("unexpected envelope field %q", key)
		}
	}
}

func TestIsRedirectNil(t *testing.T) {
	t.Parallel()
	var e *CLIError
	if e.IsRedirect() {
		t.Error("nil IsRedirect() = true")
	}
}
