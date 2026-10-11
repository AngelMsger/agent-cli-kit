package errors

import "sync/atomic"

// Guidance returns the default hint and next steps for a category. Each CLI
// registers its own with SetGuidance, because hints name product commands and
// product nouns; New applies it to every error it builds.
type Guidance func(cat Category) (hint string, steps []string)

var guidance atomic.Pointer[Guidance]

// SetGuidance installs the product's default guidance. A nil g restores the
// generic guidance. Call it once during start-up, before any error is built.
func SetGuidance(g Guidance) {
	if g == nil {
		guidance.Store(nil)
		return
	}
	guidance.Store(&g)
}

func defaultGuidance(cat Category) (string, []string) {
	if g := guidance.Load(); g != nil {
		return (*g)(cat)
	}
	return GenericGuidance(cat)
}

// GenericGuidance is the product-neutral guidance used until a CLI installs its
// own. A product Guidance may fall back to it for categories it does not tailor.
func GenericGuidance(cat Category) (hint string, steps []string) {
	switch cat {
	case CategoryUsage:
		return "The command was invoked incorrectly. Check flags and arguments.",
			[]string{"Run the command with --help."}
	case CategoryConfig:
		return "No usable configuration was found or it is invalid.",
			[]string{"Run `config init`, or `config show` to inspect the active configuration."}
	case CategoryAuth:
		return "The server rejected the credentials. They may be expired or wrong.",
			[]string{"Run `auth status`, or `config init` to store new credentials."}
	case CategoryPermission:
		return "The credentials are valid but lack permission for this resource.",
			[]string{"Verify the account can access the resource in a browser."}
	case CategoryNotFound:
		return "The requested resource does not exist.",
			[]string{"Double-check the key, ID or URL."}
	case CategoryConflict:
		return "The resource changed since it was last read (version conflict).",
			[]string{"Re-fetch the resource to get its current state, then retry."}
	case CategoryRateLimit:
		return "The server is rate limiting requests.",
			[]string{"Retry reads after a short wait; verify a write's outcome before repeating it."}
	case CategoryNetwork:
		return "The server could not be reached (DNS, TLS or timeout).",
			[]string{"Run `doctor`, and check --base-url and network connectivity."}
	case CategoryServer:
		return "The server returned an internal error.",
			[]string{"Retry reads later; verify a write's outcome before repeating it."}
	case CategoryParse:
		return "A response could not be parsed or rendered.",
			[]string{"Inspect the error code and verify remote state with a read before repeating a write."}
	default:
		return "An unexpected internal error occurred.",
			[]string{"Retry with --verbose for details."}
	}
}
