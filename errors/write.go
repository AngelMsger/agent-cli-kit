package errors

import "strings"

// A write changes remote state. When it fails, the change may still have
// happened, so repeating it can apply it twice: a second comment, a second
// build. The family rule is that an uncertain write is reconciled with a read
// before anything replays it, and that a write the server acknowledged keeps
// the identity of what it changed even when a later step fails. FromWrite and
// WriteSucceeded apply that rule, so every CLI reports these cases with the
// same codes, hint and recovery shape; a CLI supplies only the WriteTarget.

// Codes for a write the server acknowledged whose result the CLI cannot
// deliver. The error keeps the category and HTTP status of what failed.
const (
	// CodeWriteSucceededResponseInvalid: the response could not be decoded or
	// omitted the identity of what the write created or changed.
	CodeWriteSucceededResponseInvalid = "WRITE_SUCCEEDED_RESPONSE_INVALID"
	// CodeWriteSucceededReadFailed: reading the result back failed.
	CodeWriteSucceededReadFailed = "WRITE_SUCCEEDED_READ_FAILED"
	// CodeWriteResponseMissingID is the cause wrapped by MissingWriteIdentity.
	CodeWriteResponseMissingID = "WRITE_RESPONSE_MISSING_ID"
)

// writeHint is the agent-facing instruction on every write-outcome error.
const writeHint = "Do not repeat the write. Inspect the remote state using the read command; " +
	"follow pagination if needed. An empty page is not proof that the write failed."

// redirectWriteHint replaces writeHint when the server answered the write
// with a redirect, which the transport never follows for a write.
const redirectWriteHint = "Do not repeat the write. The server answered it with a redirect, " +
	"which usually means a login page, a single sign-on proxy, or a base URL that should use https. " +
	"Check the configured base URL and run doctor, then inspect the remote state using the read command."

// WriteTarget names what a write changes, for the error message, and how to
// see its current state, for the next steps.
type WriteTarget struct {
	// Description identifies the target, for example
	// "comment on issue ENG-1 (https://jira.example/browse/ENG-1)".
	Description string
	// ReadSteps are bounded, read-only commands that show the target's state,
	// such as "jira-cli comment list 'ENG-1' --limit 25". Quote user-supplied
	// values with ShellArg. A write has at least one.
	ReadSteps []string
}

// FromWrite classifies the error a write request returned. A nil error stays
// nil. Only a definite refusal, which shows the write was not applied, is
// returned unchanged, so its guidance and retryable flag still apply: a
// usage, config, auth, permission, not-found or conflict error. Everything
// else leaves the outcome unknown and is rewrapped so that nothing replays
// it: network, server, rate-limit, parse and internal errors, and any error
// that is not a CLIError, such as a raw context.DeadlineExceeded or
// *url.Error. One exception: a 2xx response that could not be decoded was
// acknowledged, and reports CodeWriteSucceededResponseInvalid.
//
// The rewrapped error keeps the cause's category, code, HTTP status and
// details, is not retryable, and has the target's read steps as its next
// steps. A 3xx is one of these unknown outcomes: the transport returns a
// write's redirect instead of following it, and its hint says what a
// redirect usually means.
//
// The category is read from the outermost CLIError in err's chain, as
// AsCLIError finds it. A CLI that wraps a transport failure in a refusal
// category, or joins a refusal before it, hides that failure from FromWrite;
// report a write the server confirmed with WriteSucceeded instead.
func FromWrite(err error, target WriteTarget) error {
	if err == nil {
		return nil
	}
	ce := AsCLIError(err)
	switch ce.Category {
	case CategoryUsage, CategoryConfig, CategoryAuth, CategoryPermission, CategoryNotFound, CategoryConflict:
		return err
	case CategoryParse:
		if ce.HTTPStatus >= 200 && ce.HTTPStatus < 300 {
			return WriteSucceeded(err, CodeWriteSucceededResponseInvalid, "its response could not be decoded", target)
		}
	}
	out := writeOutcome(err, ce.Code, "Write outcome is unknown for "+target.Description, target)
	if ce.HTTPStatus >= 300 && ce.HTTPStatus < 400 {
		out.Hint = redirectWriteHint
	}
	return out
}

// WriteSucceeded reports a write the server acknowledged whose result cannot
// be delivered, because problem happened: "its response was incomplete",
// "reading the updated issue failed". code is one of the CodeWriteSucceeded*
// constants or a CLI's own WRITE_SUCCEEDED_* code. The error keeps cause as
// its cause and takes its category, HTTP status and details, so the exit code
// tells what went wrong after the write. It is not retryable, and its next
// steps are the target's read steps.
func WriteSucceeded(cause error, code, problem string, target WriteTarget) *CLIError {
	return writeOutcome(cause, code, "Write succeeded for "+target.Description+", but "+problem, target)
}

// MissingWriteIdentity reports an acknowledged write whose response omitted
// the identifier of what it created or changed, such as a Jenkins queue
// location or a new comment's ID.
func MissingWriteIdentity(target WriteTarget) *CLIError {
	cause := New(CategoryParse, CodeWriteResponseMissingID,
		"the successful write response omitted its resource identifier")
	return WriteSucceeded(cause, CodeWriteSucceededResponseInvalid, "its response was incomplete", target)
}

func writeOutcome(err error, code, message string, target WriteTarget) *CLIError {
	cause := AsCLIError(err)
	out := Wrap(err, cause.Category, code, message+": "+cause.Message).
		WithHTTPStatus(cause.HTTPStatus).
		WithDetails(cause.Details).
		WithHint(writeHint).
		WithNextSteps(target.ReadSteps...)
	out.Retryable = false
	return out
}

// ShellArg quotes value as one word for a POSIX shell (sh, bash, zsh), for the
// commands an error suggests as next steps: ShellArg("it's") is 'it'"'"'s'.
// The result is not valid quoting for cmd.exe or PowerShell.
func ShellArg(value string) string {
	return "'" + strings.ReplaceAll(value, "'", `'"'"'`) + "'"
}
