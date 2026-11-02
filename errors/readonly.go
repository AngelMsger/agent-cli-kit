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
// command is refused the same way.
//
// Read-only mode is the user's safety setting, and agents act on next steps,
// so the steps never turn it off. The steps are --dry-run (when the CLI has
// it) and --allow-writes, conditioned on the user authorizing this write; the
// standard allows only that one-invocation override. The hint names the
// switches the user set, as the user's to change.
func ReadOnlyBlocked(op string, mode ReadOnlyMode) *CLIError {
	var steps []string
	if mode.DryRun {
		steps = append(steps, "Add --dry-run to preview the request without sending it")
	}
	steps = append(steps, "Only if the user authorized this write, re-run with --allow-writes")
	return New(CategoryPermission, CodeReadOnlyBlocked,
		fmt.Sprintf("operation %q blocked: read-only mode is enabled", op)).
		WithHint("Read-only mode is on, so nothing was sent. The user turned it on" + switches(mode) +
			", and only the user turns it off. --allow-writes lifts it for one invocation and is for a write the user authorized.").
		WithNextSteps(steps...)
}

// switches names where read-only mode can be on, without claiming that
// changing one of them turns it off: the other may still be set.
func switches(mode ReadOnlyMode) string {
	switch {
	case mode.Env != "" && mode.ConfigFile != "":
		return " (" + mode.Env + " or defaults.read_only in " + mode.ConfigFile + ")"
	case mode.Env != "":
		return " (" + mode.Env + " or defaults.read_only)"
	case mode.ConfigFile != "":
		return " (defaults.read_only in " + mode.ConfigFile + ")"
	}
	return ""
}
