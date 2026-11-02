package errors

import (
	"encoding/json"
	"testing"
)

// TestReadOnlyBlockedWireFormat pins the one READONLY_BLOCKED wording every
// CLI prints, in its fullest form.
func TestReadOnlyBlockedWireFormat(t *testing.T) {
	err := ReadOnlyBlocked("trigger a build", ReadOnlyMode{
		Env: "TOOL_CLI_READ_ONLY", ConfigFile: "~/.angelmsger/tool/config.yaml", DryRun: true,
	})
	if ExitCode(err) != ExitPermission || err.Retryable {
		t.Fatalf("exit %d, retryable %v; want %d, false", ExitCode(err), err.Retryable, ExitPermission)
	}
	got, jerr := json.Marshal(err.Payload())
	if jerr != nil {
		t.Fatal(jerr)
	}
	const want = `{"error":{"category":"permission","code":"READONLY_BLOCKED",` +
		`"message":"operation \"trigger a build\" blocked: read-only mode is enabled",` +
		`"hint":"Read-only mode is on, so nothing was sent. The user turned it on ` +
		`(TOOL_CLI_READ_ONLY or defaults.read_only in ~/.angelmsger/tool/config.yaml), and only the user turns it off. ` +
		`--allow-writes lifts it for one invocation and is for a write the user authorized.",` +
		`"next_steps":["Add --dry-run to preview the request without sending it",` +
		`"Only if the user authorized this write, re-run with --allow-writes"],` +
		`"retryable":false}}`
	if string(got) != want {
		t.Fatalf("payload JSON changed:\n got %s\nwant %s", got, want)
	}
}

// A CLI names only the switches it has.
func TestReadOnlyBlockedNamesOnlyTheCLIsSwitches(t *testing.T) {
	err := ReadOnlyBlocked("CreateAlert", ReadOnlyMode{})
	const hint = "Read-only mode is on, so nothing was sent. The user turned it on, and only the user turns it off. " +
		"--allow-writes lifts it for one invocation and is for a write the user authorized."
	if err.Hint != hint || len(err.NextSteps) != 1 || err.NextSteps[0] != "Only if the user authorized this write, re-run with --allow-writes" {
		t.Fatalf("hint %q, steps %q", err.Hint, err.NextSteps)
	}
	env := ReadOnlyBlocked("CreateAlert", ReadOnlyMode{Env: "TOOL_CLI_READ_ONLY"})
	if want := "Read-only mode is on, so nothing was sent. The user turned it on (TOOL_CLI_READ_ONLY or defaults.read_only), " +
		"and only the user turns it off. --allow-writes lifts it for one invocation and is for a write the user authorized."; env.Hint != want {
		t.Fatalf("hint %q, want %q", env.Hint, want)
	}
}
