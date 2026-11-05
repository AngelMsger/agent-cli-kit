package errors

import (
	"fmt"
	"net/url"
)

// RedirectGuide is what a CLI tells its user about a redirect the transport
// returned: where the server URL is set, and how to check it.
type RedirectGuide struct {
	// Setting names where the user sets the server URL, as the hint should
	// say it: "--base-url", or "the server URL (WECOM_CALENDAR_SERVER or the
	// context's server)".
	Setting string
	// Steps are the next steps, the CLI's doctor first.
	Steps []string
}

// WithRedirect applies the family's rule for a 3xx answer to e, the error a
// CLI built for that response, and returns e. The transport returns a
// redirect instead of following it when following would answer a different
// request (any write, or a read whose method a 301, 302 or 303 would turn
// into a GET), when it leaves the origin under SameOriginRedirects, or when
// the chain reaches 10 hops. Sending the same request again is redirected the
// same way, so e is not retryable, whatever its category.
//
// location is the response's Location, resolved against its request as
// http.Response.Location returns it, or nil when it has none. With a
// location, the hint says where the redirect points (scheme, host and path
// only: a query can carry a sign-in token) and what a redirect usually
// means. Without one (a 300 or 304, or a redirect without a Location), it
// says the same request gets the same answer. Either way it names
// g.Setting, and g.Steps become the next steps. e keeps its category, code
// and HTTP status. For a write, FromWrite later puts its own hint and the
// target's read steps in their place.
func (e *CLIError) WithRedirect(location *url.URL, g RedirectGuide) *CLIError {
	if location != nil {
		e.Hint = fmt.Sprintf("The server redirected the request to %s: usually a login page, "+
			"a single sign-on proxy, or a server URL that should use https. The same request is "+
			"redirected again, so check %s first.", redirectTarget(location), g.Setting)
	} else {
		e.Hint = fmt.Sprintf("The server answered with HTTP %d and no redirect target, which this "+
			"request does not expect. The same request gets the same answer, so check %s first.",
			e.HTTPStatus, g.Setting)
	}
	e.NextSteps = g.Steps
	e.Retryable = false
	e.redirect = location != nil
	return e
}

// IsRedirect reports whether WithRedirect classified e as a redirect to a
// location, as a doctor check reports it.
func (e *CLIError) IsRedirect() bool { return e != nil && e.redirect }

// redirectTarget is location without its user information, query and
// fragment, which can carry credentials or a sign-in token.
func redirectTarget(location *url.URL) string {
	return (&url.URL{Scheme: location.Scheme, Host: location.Host, Path: location.Path}).String()
}
