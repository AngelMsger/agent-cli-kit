// Package auth models a CLI's credentials: the schemes it accepts, how a
// credential goes on the wire, where its secret is stored, and the errors that
// tell an agent how to recover when the secret cannot be read.
//
// Nothing here names a product. Each CLI calls Register once at start-up with
// its schemes and the wording of its recovery steps, the same way it installs
// its error hints with errors.SetGuidance.
package auth

import (
	"strings"
	"sync/atomic"

	"github.com/angelmsger/agent-cli-kit/transport"
)

// Kind sets a scheme's default Authorization header and what Validate
// requires of a credential.
type Kind int

const (
	// None sends no Authorization header and needs no secret.
	None Kind = iota
	// Basic sends "Basic base64(username:secret)" and needs both.
	Basic
	// Bearer sends "Bearer secret" and needs the secret.
	Bearer
	// Custom needs a secret and sends nothing unless the scheme sets Header
	// or Decorate.
	Custom
)

// Scheme declares one auth scheme a CLI accepts.
type Scheme struct {
	// Name is the value users configure, such as "pat" or "basic".
	Name string
	Kind Kind
	// MissingCode and Missing are the error code and message Validate returns
	// when the username or secret the Kind needs is absent. MissingCode
	// defaults to AUTH_NO_BASIC for Basic and AUTH_NO_TOKEN otherwise.
	MissingCode string
	Missing     string
	// Header, when set, replaces the Authorization value the Kind builds.
	Header func(Credential) string
	// Validate, when set, replaces the check the Kind implies.
	Validate func(Credential) error
	// Decorate, when set, replaces the decorator that sets Authorization.
	Decorate func(Credential) transport.Decorator
	// Session marks a secret that a browser or desktop host captured and
	// refreshes, and that expires. Credential reuse prefers every other
	// scheme to it.
	Session bool
}

// Product is what a CLI registers: its schemes, and the product wording the
// recovery errors need.
type Product struct {
	Schemes []Scheme
	// DefaultScheme is used when the configuration names none.
	DefaultScheme string
	// Name is the product noun in messages, as in "stored <Name> credentials
	// are missing".
	Name string
	// Doctor is the diagnostic command offered after the host retry, such as
	// "tool doctor".
	Doctor string
	// MissingStep is the last step when no stored secret is visible.
	// Request.MissingSteps replaces it for one call.
	MissingStep string
	// InaccessibleStep is the last step when the store cannot be read.
	InaccessibleStep string
	// MismatchHint and MismatchSteps, when set, replace the guidance on
	// CREDENTIAL_SERVICE_MISMATCH.
	MismatchHint  string
	MismatchSteps []string
	// ListSchemes names the accepted schemes in AUTH_BAD_SCHEME.
	ListSchemes bool
	// AccountKeyIncludesPath keys stored secrets by host and URL path instead
	// of host alone. Turning it on for a CLI that stored secrets without it
	// orphans them, so only a CLI that always keyed by path sets it.
	AccountKeyIncludesPath bool
}

var registered atomic.Pointer[Product]

// Register installs the product's schemes and wording. Call it once during
// start-up, before any credential is validated or resolved. A nil p restores
// the empty registration, under which every scheme is unknown.
func Register(p *Product) {
	if p == nil {
		registered.Store(nil)
		return
	}
	c := *p
	c.Schemes = append([]Scheme(nil), p.Schemes...)
	c.MismatchSteps = append([]string(nil), p.MismatchSteps...)
	registered.Store(&c)
}

func product() *Product {
	if p := registered.Load(); p != nil {
		return p
	}
	return &Product{}
}

func lookup(name string) (Scheme, bool) {
	for _, s := range product().Schemes {
		if s.Name == name {
			return s, true
		}
	}
	return Scheme{}, false
}

// schemeList joins the registered scheme names as "a, b or c".
func schemeList() string {
	schemes := product().Schemes
	names := make([]string, len(schemes))
	for i, s := range schemes {
		names[i] = s.Name
	}
	if len(names) < 2 {
		return strings.Join(names, "")
	}
	return strings.Join(names[:len(names)-1], ", ") + " or " + names[len(names)-1]
}

// UsesUsername reports whether the named scheme sends a username when it
// authenticates. Under any other scheme a stored username is only a label.
func UsesUsername(name string) bool {
	s, ok := lookup(name)
	return ok && s.Kind == Basic
}

// IsSession reports whether the named scheme is a captured browser session.
func IsSession(name string) bool {
	s, ok := lookup(name)
	return ok && s.Session
}

// NeedsCredential reports whether the named scheme is registered and needs a
// stored secret, which is what makes it a candidate for credential reuse.
func NeedsCredential(name string) bool {
	s, ok := lookup(name)
	return ok && s.Kind != None
}

// SchemeNames lists the registered schemes in registration order.
func SchemeNames() []string {
	schemes := product().Schemes
	names := make([]string, len(schemes))
	for i, s := range schemes {
		names[i] = s.Name
	}
	return names
}
