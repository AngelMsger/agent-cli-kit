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
	loc, _ := url.Parse("https://user:secret@sso.example.com/login.jsp;jsessionid=8F3A2C/start?token=abc#frag")
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
	if !strings.Contains(e.Hint, "redirected the request to https://sso.example.com:") {
		t.Errorf("hint does not name the target: %q", e.Hint)
	}
	for _, leaked := range []string{"secret", "user", "token", "abc", "frag", "jsessionid", "8F3A2C", "login.jsp"} {
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

// An unset setting or status still reads as a sentence.
func TestWithRedirectFallbacks(t *testing.T) {
	t.Parallel()
	e := New(CategoryServer, "X", "msg").WithRedirect(nil, RedirectGuide{})
	if !strings.Contains(e.Hint, "answered with a 3xx status") || !strings.Contains(e.Hint, "check the server URL first") {
		t.Errorf("hint = %q", e.Hint)
	}
	rel := &url.URL{Path: "/login"}
	e = New(CategoryServer, "X", "msg").WithHTTPStatus(302).WithRedirect(rel, RedirectGuide{})
	if !strings.Contains(e.Hint, "redirected the request to another path on the same server:") {
		t.Errorf("relative location hint = %q", e.Hint)
	}
}

// Appending to one error's next steps leaves the shared guide alone.
func TestWithRedirectCopiesTheSteps(t *testing.T) {
	t.Parallel()
	guide := RedirectGuide{Setting: "--base-url", Steps: make([]string, 1, 4)}
	guide.Steps[0] = "example-cli doctor"
	e := New(CategoryServer, "X", "msg").WithHTTPStatus(302).WithRedirect(nil, guide)
	e.NextSteps = append(e.NextSteps, "example-cli status flags")
	other := New(CategoryServer, "X", "msg").WithHTTPStatus(302).WithRedirect(nil, guide)
	other.NextSteps = append(other.NextSteps, "example-cli other")
	if e.NextSteps[1] != "example-cli status flags" || len(guide.Steps) != 1 {
		t.Fatalf("steps = %v, guide = %v", e.NextSteps, guide.Steps)
	}
}
