package cliflags

import (
	"encoding/json"
	"testing"
)

// TestCorrectionWireFormat pins the corrections a CLI echoes in its _notice, so
// an agent can see how its argv was rewritten.
func TestCorrectionWireFormat(t *testing.T) {
	got, err := json.Marshal([]Correction{
		{Original: "--max", Corrected: "--limit", Kind: "flag-name"},
		{Kind: "escape", Flag: "--body", Detail: "interpreted 2 escape sequences"},
	})
	if err != nil {
		t.Fatal(err)
	}
	const want = `[{"original":"--max","corrected":"--limit","kind":"flag-name"},` +
		`{"kind":"escape","flag":"--body","detail":"interpreted 2 escape sequences"}]`
	if string(got) != want {
		t.Fatalf("correction JSON changed:\n got %s\nwant %s", got, want)
	}
}
