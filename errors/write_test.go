package errors

import (
	"context"
	"encoding/json"
	stderrors "errors"
	"net/url"
	"testing"
)

var testTarget = WriteTarget{
	Description: "comment on issue ENG-1 (https://jira.example/browse/ENG-1)",
	ReadSteps:   []string{"tool comment list 'ENG-1' --limit 25"},
}

// assertWriteOutcome checks the shape every write-outcome error shares: the
// cause's category and HTTP status, never retryable, the write hint, the
// target's read steps, and the original error still reachable.
func assertWriteOutcome(t *testing.T, err, cause error, code, message string) {
	t.Helper()
	ce := AsCLIError(err)
	from := AsCLIError(cause)
	if ce.Code != code || ce.Message != message {
		t.Fatalf("code, message = %q, %q; want %q, %q", ce.Code, ce.Message, code, message)
	}
	if ce.Category != from.Category || ce.HTTPStatus != from.HTTPStatus {
		t.Fatalf("category, status = %s, %d; want the cause's %s, %d", ce.Category, ce.HTTPStatus, from.Category, from.HTTPStatus)
	}
	if ce.Retryable || ce.Hint != writeHint || len(ce.NextSteps) != 1 || ce.NextSteps[0] != testTarget.ReadSteps[0] {
		t.Fatalf("recovery = retryable %v, hint %q, steps %q", ce.Retryable, ce.Hint, ce.NextSteps)
	}
	if !stderrors.Is(err, cause) {
		t.Fatal("the original error is no longer reachable")
	}
}

func TestFromWriteUnknownOutcome(t *testing.T) {
	transportErr := stderrors.New("connection reset after the request was sent")
	causes := map[string]error{
		"network":       Wrap(transportErr, CategoryNetwork, "NETWORK", "request failed"),
		"server":        New(CategoryServer, "HTTP_503", "temporarily unavailable").WithHTTPStatus(503),
		"rate limit":    New(CategoryRateLimit, "HTTP_429", "slow down").WithHTTPStatus(429),
		"redirect":      New(CategoryServer, "HTTP_302", "unexpected redirect").WithHTTPStatus(302),
		"non-2xx parse": New(CategoryParse, "DECODE", "bad error body").WithHTTPStatus(502),
		"internal":      New(CategoryInternal, "INTERNAL", "unexpected state"),
		// Errors a caller passes in without classifying them first.
		"raw deadline": context.DeadlineExceeded,
		"raw canceled": context.Canceled,
		"raw url.Error": &url.Error{Op: "Post", URL: "https://jira.example/rest/api/2/issue",
			Err: stderrors.New("connection reset by peer")},
	}
	for name, cause := range causes {
		t.Run(name, func(t *testing.T) {
			err := FromWrite(cause, testTarget)
			ce := AsCLIError(cause)
			assertWriteOutcome(t, err, cause, ce.Code,
				"Write outcome is unknown for "+testTarget.Description+": "+ce.Message)
		})
	}
}

func TestFromWriteAcknowledgedButUndecodable(t *testing.T) {
	cause := New(CategoryParse, "DECODE", "unexpected end of JSON input").WithHTTPStatus(201)
	assertWriteOutcome(t, FromWrite(cause, testTarget), cause, CodeWriteSucceededResponseInvalid,
		"Write succeeded for "+testTarget.Description+", but its response could not be decoded: unexpected end of JSON input")
}

// A definite refusal proves nothing was written, so FromWrite leaves it alone:
// its guidance, code and retryable flag reach the caller unchanged. Every other
// category, and any error that is not classified, leaves the outcome unknown.
func TestFromWriteKeepsDefiniteRefusals(t *testing.T) {
	for _, cat := range []Category{CategoryUsage, CategoryConfig, CategoryAuth, CategoryPermission,
		CategoryNotFound, CategoryConflict} {
		t.Run(string(cat), func(t *testing.T) {
			cause := New(cat, "REFUSED", "refused")
			if got := FromWrite(cause, testTarget); got != cause {
				t.Fatalf("FromWrite rewrote %s: %+v", cat, got)
			}
		})
	}
	if FromWrite(nil, testTarget) != nil {
		t.Fatal("FromWrite(nil) is not nil")
	}
}

func TestWriteSucceededKeepsTheFollowUpFailure(t *testing.T) {
	cause := New(CategoryPermission, "HTTP_403", "cannot read issue").WithHTTPStatus(403)
	err := WriteSucceeded(cause, CodeWriteSucceededReadFailed, "reading the updated issue failed", testTarget)
	assertWriteOutcome(t, err, cause, CodeWriteSucceededReadFailed,
		"Write succeeded for "+testTarget.Description+", but reading the updated issue failed: cannot read issue")
	if ExitCode(err) != ExitPermission {
		t.Fatalf("exit code %d, want the read failure's %d", ExitCode(err), ExitPermission)
	}
}

// Details are data, not retry instructions, so a rewrapped error keeps them.
func TestWriteOutcomeKeepsDetails(t *testing.T) {
	details := map[string]string{"request_id": "r-42"}
	cause := New(CategoryServer, "HTTP_503", "temporarily unavailable").WithHTTPStatus(503).WithDetails(details)
	for name, err := range map[string]error{
		"unknown":   FromWrite(cause, testTarget),
		"succeeded": WriteSucceeded(cause, CodeWriteSucceededReadFailed, "reading it back failed", testTarget),
	} {
		if got := AsCLIError(err).Details; got == nil || got.(map[string]string)["request_id"] != "r-42" {
			t.Errorf("%s: details = %v, want the cause's", name, got)
		}
	}
}

func TestMissingWriteIdentity(t *testing.T) {
	err := MissingWriteIdentity(testTarget)
	if err.Code != CodeWriteSucceededResponseInvalid || err.Category != CategoryParse || err.Retryable {
		t.Fatalf("got %+v", err)
	}
	const want = "Write succeeded for comment on issue ENG-1 (https://jira.example/browse/ENG-1), " +
		"but its response was incomplete: the successful write response omitted its resource identifier"
	if err.Message != want {
		t.Fatalf("message %q, want %q", err.Message, want)
	}
	var cause *CLIError
	if !stderrors.As(err.Unwrap(), &cause) || cause.Code != CodeWriteResponseMissingID {
		t.Fatalf("cause = %+v, want %s", cause, CodeWriteResponseMissingID)
	}
}

// TestWriteOutcomeWireFormat pins the JSON an agent parses for an uncertain
// write, the shape jira-cli has printed since before the rule moved here.
func TestWriteOutcomeWireFormat(t *testing.T) {
	cause := New(CategoryServer, "HTTP_503", "temporarily unavailable").WithHTTPStatus(503)
	got, err := json.Marshal(AsCLIError(FromWrite(cause, testTarget)).Payload())
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"category":"server","code":"HTTP_503",` +
		`"message":"Write outcome is unknown for comment on issue ENG-1 (https://jira.example/browse/ENG-1): temporarily unavailable",` +
		`"hint":"Do not repeat the write. Inspect the remote state using the read command; follow pagination if needed. An empty page is not proof that the write failed.",` +
		`"next_steps":["tool comment list 'ENG-1' --limit 25"],"retryable":false,"http_status":503}}`
	if string(got) != want {
		t.Fatalf("payload JSON changed:\n got %s\nwant %s", got, want)
	}
}

func TestShellArg(t *testing.T) {
	for in, want := range map[string]string{
		"ENG-1":        "'ENG-1'",
		"":             "''",
		"it's":         `'it'"'"'s'`,
		"a b; rm -rf ": "'a b; rm -rf '",
		"$HOME `x`":    "'$HOME `x`'",
	} {
		if got := ShellArg(in); got != want {
			t.Errorf("ShellArg(%q) = %s, want %s", in, got, want)
		}
	}
}
