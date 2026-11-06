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
// location, the hint says where the redirect points (its scheme and host
// only: a path can carry a session ID, as in ";jsessionid=", and a query a
// sign-in token) and what a redirect usually means. Without one (a 300 or
// 304, or a redirect without a Location), it says the same request gets the
// same answer, naming e's HTTP status, so call WithHTTPStatus first. Either
// way it names g.Setting ("the server URL" when empty), and g.Steps become
// the next steps. e keeps its category, code and HTTP status. For a write,
// FromWrite later puts its own hint and the target's read steps in their
// place.
func (e *CLIError) WithRedirect(location *url.URL, g RedirectGuide) *CLIError {
	setting := g.Setting
	if setting == "" {
		setting = "the server URL"
	}
	if location != nil {
		e.Hint = fmt.Sprintf("The server redirected the request to %s: usually a login page, "+
			"a single sign-on proxy, or a server URL that should use https. The same request is "+
			"redirected again, so check %s first.", redirectTarget(location), setting)
	} else {
		status := "a 3xx status"
		if e.HTTPStatus != 0 {
			status = fmt.Sprintf("HTTP %d", e.HTTPStatus)
		}
		e.Hint = fmt.Sprintf("The server answered with %s and no redirect target, which this "+
			"request does not expect. The same request gets the same answer, so check %s first.",
			status, setting)
	}
	// A copy, so a caller that appends a step does not write into a guide
	// that every error shares.
	e.NextSteps = append([]string(nil), g.Steps...)
	e.Retryable = false
	e.redirect = location != nil
	return e
}

// IsRedirect reports whether WithRedirect classified e as a redirect to a
// location, as a doctor check reports it.
func (e *CLIError) IsRedirect() bool { return e != nil && e.redirect }

// redirectTarget is location's scheme and host. Its user information, path,
// query and fragment can carry credentials, a session ID or a sign-in token.
// A relative location, which the caller did not resolve, has neither and
// reads as "another path on the same server".
func redirectTarget(location *url.URL) string {
	if location.Host == "" {
		return "another path on the same server"
	}
	return (&url.URL{Scheme: location.Scheme, Host: location.Host}).String()
}
