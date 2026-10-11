package auth

import (
	"encoding/json"
	"errors"
	"testing"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

// TestRecoveryWireFormat pins the payloads an agent acts on when a stored
// credential cannot be read. Agents branch on the code and the recovery
// object, so change them only together with each CLI's docs.
func TestRecoveryWireFormat(t *testing.T) {
	testProduct(t, nil)

	cases := []struct {
		name string
		err  *cerrors.CLIError
		want string
	}{
		{"missing", missingError(nil),
			`{"error":{"category":"config","code":"CREDENTIAL_NOT_VISIBLE_OR_MISSING",` +
				`"message":"stored Widget credentials are missing or not visible in this execution environment",` +
				`"hint":"An agent sandbox may have a different home or keychain view even when the host is already configured.",` +
				`"next_steps":["Retry the same command with access to the host user environment (home directory and OS keychain).","tool doctor","Run ` + "`tool config init`" + `."],` +
				`"retryable":false,` +
				`"recovery":{"action":"retry_current_command","scope":"host","requires":["user_home","os_keychain"]}}}`},
		{"inaccessible", inaccessibleError(errors.New("denied")),
			`{"error":{"category":"config","code":"CREDENTIAL_STORE_INACCESSIBLE",` +
				`"message":"stored Widget credentials cannot be read in this execution environment",` +
				`"hint":"The configured credential store is inaccessible; this commonly happens when an agent sandbox cannot access the host keychain or credential file.",` +
				`"next_steps":["Retry the same command with access to the host user environment (home directory and OS keychain).","tool doctor","Do not run ` + "`config init`" + ` yet."],` +
				`"retryable":false,` +
				`"recovery":{"action":"retry_current_command","scope":"host","requires":["user_home","os_keychain"]}}}`},
	}
	for _, tc := range cases {
		got, err := json.Marshal(tc.err.Payload())
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != tc.want {
			t.Errorf("%s payload JSON changed:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}
