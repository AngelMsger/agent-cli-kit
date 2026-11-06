// Package doctor holds the rules every CLI's doctor command shares: how the
// error a check failed with becomes that check's status, recovery scope and
// detail in the report. The report's shape, its checks and its next steps
// stay with each CLI.
//
// The status words are an agent-facing contract: add new ones rather than
// renaming or repurposing existing ones.
package doctor

import cerrors "github.com/angelmsger/agent-cli-kit/errors"

// Status words a check reports.
const (
	StatusOK = "ok"
	// StatusInaccessible: the credential store could not be read here (an
	// agent sandbox without the user's home or keychain, say).
	StatusInaccessible = "inaccessible"
	// StatusMissingOrInaccessible: no credential is visible from here.
	StatusMissingOrInaccessible = "missing_or_inaccessible"
	// StatusRejectedByServer: the server refused the credential.
	StatusRejectedByServer = "rejected_by_server"
	// StatusRedirected: the server answered with a redirect the CLI does not
	// follow, usually to a login page or to https. The server is reachable;
	// its URL is what needs checking.
	StatusRedirected = "redirected"
	// StatusUnreachable: a network failure or a server error.
	StatusUnreachable = "unreachable"
	// StatusInvalid: anything else, such as a malformed setting.
	StatusInvalid = "invalid"
)

// Status is the status of a check that ended with err: StatusOK for nil.
// A redirect is reported by what errors.CLIError.WithRedirect recorded, so a
// CLI builds its HTTP errors with it.
func Status(err error) string {
	if err == nil {
		return StatusOK
	}
	ce := cerrors.AsCLIError(err)
	switch ce.Code {
	case "CREDENTIAL_STORE_INACCESSIBLE":
		return StatusInaccessible
	case "CREDENTIAL_NOT_VISIBLE_OR_MISSING":
		return StatusMissingOrInaccessible
	}
	if ce.IsRedirect() {
		return StatusRedirected
	}
	switch ce.Category {
	case cerrors.CategoryAuth, cerrors.CategoryPermission:
		return StatusRejectedByServer
	case cerrors.CategoryNetwork, cerrors.CategoryServer:
		return StatusUnreachable
	default:
		return StatusInvalid
	}
}

// RecoveryScope is the scope of err's structured recovery ("host" when the
// check needs the user's own environment), or "" when it has none.
func RecoveryScope(err error) string {
	if err == nil {
		return ""
	}
	if recovery := cerrors.AsCLIError(err).Recovery; recovery != nil {
		return recovery.Scope
	}
	return ""
}

// Detail is the detail of a check that ended with err: its message. For a
// redirect it adds the hint, which says where the redirect points and which
// setting to check, since the report carries no hint of its own.
func Detail(err error) string {
	if err == nil {
		return ""
	}
	ce := cerrors.AsCLIError(err)
	if ce.IsRedirect() && ce.Hint != "" {
		return ce.Message + ". " + ce.Hint
	}
	return ce.Message
}
