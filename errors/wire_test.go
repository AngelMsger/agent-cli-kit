package errors

import (
	"encoding/json"
	"testing"
)

// TestPayloadWireFormat pins the JSON an agent parses on failure. Every CLI in
// the family emits this envelope, so a renamed or reordered key here breaks
// every caller at once; change it only together with each CLI's docs.
func TestPayloadWireFormat(t *testing.T) {
	t.Cleanup(func() { SetGuidance(nil) })
	SetGuidance(func(Category) (string, []string) { return "Check the key.", []string{"tool get --help"} })

	e := New(CategoryNotFound, "NOT_FOUND", "widget W-1 not found").
		WithHTTPStatus(404).
		WithRecovery(Recovery{Action: "retry_current_command", Scope: "host", Requires: []string{"os_keychain"}}).
		WithDetails(map[string]string{"key": "W-1"})
	got, err := json.Marshal(e.Payload())
	if err != nil {
		t.Fatal(err)
	}
	const want = `{"error":{"details":{"key":"W-1"},"category":"not_found","code":"NOT_FOUND",` +
		`"message":"widget W-1 not found","hint":"Check the key.","next_steps":["tool get --help"],` +
		`"retryable":false,"http_status":404,` +
		`"recovery":{"action":"retry_current_command","scope":"host","requires":["os_keychain"]}}}`
	if string(got) != want {
		t.Fatalf("payload JSON changed:\n got %s\nwant %s", got, want)
	}

	minimal, err := json.Marshal(New(CategoryInternal, "INTERNAL", "boom").WithHint("").WithNextSteps().Payload())
	if err != nil {
		t.Fatal(err)
	}
	const wantMinimal = `{"error":{"category":"internal","code":"INTERNAL","message":"boom","retryable":false}}`
	if string(minimal) != wantMinimal {
		t.Fatalf("minimal payload JSON changed:\n got %s\nwant %s", minimal, wantMinimal)
	}
}
