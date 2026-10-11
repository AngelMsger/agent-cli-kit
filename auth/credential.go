package auth

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"strconv"
	"strings"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
	"github.com/angelmsger/agent-cli-kit/transport"
)

// Credential is a fully resolved credential ready to authenticate requests.
type Credential struct {
	Scheme   string
	Username string // for schemes that send one, such as Basic
	Secret   string
}

// NeedsSecret reports whether the scheme needs a stored secret at all. Only a
// registered None scheme does not, so nothing prompts for its secret, writes
// it to the keychain, or fails when the keychain is unreachable.
func (c Credential) NeedsSecret() bool {
	s, ok := lookup(c.Scheme)
	return !ok || s.Kind != None
}

// Validate reports whether the credential is internally consistent.
func (c Credential) Validate() error {
	s, ok := lookup(c.Scheme)
	if !ok {
		message := "unknown auth scheme " + strconv.Quote(c.Scheme)
		if product().ListSchemes {
			message += " (want " + schemeList() + ")"
		}
		return cerrors.New(cerrors.CategoryConfig, "AUTH_BAD_SCHEME", message)
	}
	if s.Validate != nil {
		return s.Validate(c)
	}
	switch s.Kind {
	case None:
		return nil
	case Basic:
		if c.Username != "" && c.Secret != "" {
			return nil
		}
	default:
		if c.Secret != "" {
			return nil
		}
	}
	return cerrors.New(cerrors.CategoryConfig, s.missingCode(), s.missingMessage())
}

func (s Scheme) missingCode() string {
	switch {
	case s.MissingCode != "":
		return s.MissingCode
	case s.Kind == Basic:
		return "AUTH_NO_BASIC"
	default:
		return "AUTH_NO_TOKEN"
	}
}

func (s Scheme) missingMessage() string {
	switch {
	case s.Missing != "":
		return s.Missing
	case s.Kind == Basic:
		return s.Name + " auth requires both a username and a secret"
	default:
		return s.Name + " auth requires a secret"
	}
}

// Header returns the Authorization header value for the credential, or "" when
// its scheme sends none.
func (c Credential) Header() string {
	s, ok := lookup(c.Scheme)
	switch {
	case !ok:
		return ""
	case s.Header != nil:
		return s.Header(c)
	case s.Kind == Basic:
		return BasicHeader(c.Username, c.Secret)
	case s.Kind == Bearer:
		return "Bearer " + c.Secret
	default:
		return ""
	}
}

// Decorator returns a transport.Decorator that authenticates every request.
// It is never nil.
func (c Credential) Decorator() transport.Decorator {
	if s, ok := lookup(c.Scheme); ok && s.Decorate != nil {
		return s.Decorate(c)
	}
	header := c.Header()
	return func(req *http.Request) {
		if header != "" {
			req.Header.Set("Authorization", header)
		}
	}
}

// BasicHeader encodes an HTTP Basic Authorization value.
func BasicHeader(username, secret string) string {
	return "Basic " + base64.StdEncoding.EncodeToString([]byte(username+":"+secret))
}

// Redacted returns a copy safe for logging: the secret is masked.
func (c Credential) Redacted() Credential {
	c.Secret = maskSecret(c.Secret)
	return c
}

func maskSecret(s string) string {
	if s == "" {
		return ""
	}
	if len(s) <= 4 {
		return "****"
	}
	return strings.Repeat("*", len(s)-4) + s[len(s)-4:]
}

// AccountKey derives the keychain account identifier for a base URL and
// scheme. It is stable across runs so credentials can be located later.
func AccountKey(baseURL, scheme string) string {
	host := baseURL
	if u, err := url.Parse(baseURL); err == nil && u.Host != "" {
		host = u.Host
		if p := strings.Trim(u.Path, "/"); p != "" && product().AccountKeyIncludesPath {
			// A path-routed deployment (one host serving several services
			// under different prefixes) needs its own entry, not the host's.
			host += "/" + p
		}
	}
	return host + ":" + scheme
}
