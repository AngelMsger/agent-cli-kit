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
		`"hint":"Read-only mode is on, so nothing was sent. Preview the request with --dry-run. ` +
		`Re-run with --allow-writes only when this write is intended; it lifts read-only mode for one invocation.",` +
		`"next_steps":["Add --dry-run to preview the request without sending it",` +
		`"Add --allow-writes to permit writes for this invocation",` +
		`"Unset TOOL_CLI_READ_ONLY to turn read-only mode off",` +
		`"Set defaults.read_only=false in ~/.angelmsger/tool/config.yaml to turn read-only mode off"],` +
		`"retryable":false}}`
	if string(got) != want {
		t.Fatalf("payload JSON changed:\n got %s\nwant %s", got, want)
	}
}

// A CLI names only the switches it has.
func TestReadOnlyBlockedNamesOnlyTheCLIsSwitches(t *testing.T) {
	err := ReadOnlyBlocked("CreateAlert", ReadOnlyMode{})
	const hint = "Read-only mode is on, so nothing was sent. " +
		"Re-run with --allow-writes only when this write is intended; it lifts read-only mode for one invocation."
	if err.Hint != hint || len(err.NextSteps) != 1 || err.NextSteps[0] != "Add --allow-writes to permit writes for this invocation" {
		t.Fatalf("hint %q, steps %q", err.Hint, err.NextSteps)
	}
}
