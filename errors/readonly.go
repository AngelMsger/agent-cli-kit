package errors

import "fmt"

// CodeReadOnlyBlocked is the code of a write that read-only mode refused.
const CodeReadOnlyBlocked = "READONLY_BLOCKED"

// ReadOnlyMode says how a CLI switches read-only mode, so ReadOnlyBlocked can
// name the switches in the family's one wording.
type ReadOnlyMode struct {
	// Env is the environment variable that turns read-only mode on, such as
	// JIRA_CLI_READ_ONLY. Empty when the CLI has none.
	Env string
	// ConfigFile says where defaults.read_only is set, such as
	// "~/.angelmsger/jira/config.yaml". Empty when the CLI has none.
	ConfigFile string
	// DryRun is set when the CLI's write commands accept --dry-run.
	DryRun bool
}

// ReadOnlyBlocked reports that read-only mode refused op before anything was
// sent. op names the write, such as "CreateIssue" or "trigger a build". The
// error is a permission error (exit 5) and not retryable: repeating the same
// command is refused the same way. The hint says the write was not sent and
// that --allow-writes is for a write that is intended, and the next steps list
// --dry-run (when the CLI has it), --allow-writes, and the switches that turn
// the mode off.
func ReadOnlyBlocked(op string, mode ReadOnlyMode) *CLIError {
	hint := "Read-only mode is on, so nothing was sent. "
	var steps []string
	if mode.DryRun {
		hint += "Preview the request with --dry-run. "
		steps = append(steps, "Add --dry-run to preview the request without sending it")
	}
	hint += "Re-run with --allow-writes only when this write is intended; it lifts read-only mode for one invocation."
	steps = append(steps, "Add --allow-writes to permit writes for this invocation")
	if mode.Env != "" {
		steps = append(steps, "Unset "+mode.Env+" to turn read-only mode off")
	}
	if mode.ConfigFile != "" {
		steps = append(steps, "Set defaults.read_only=false in "+mode.ConfigFile+" to turn read-only mode off")
	}
	return New(CategoryPermission, CodeReadOnlyBlocked,
		fmt.Sprintf("operation %q blocked: read-only mode is enabled", op)).
		WithHint(hint).
		WithNextSteps(steps...)
}
