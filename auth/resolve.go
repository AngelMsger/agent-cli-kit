package auth

import (
	"errors"

	"github.com/angelmsger/agent-cli-kit/credstore"
	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

// BackendNone is what Save reports for a scheme that has no secret to store.
const BackendNone = "none"

// Request is one credential lookup.
type Request struct {
	// Scheme is the configured scheme; "" means the product's DefaultScheme.
	Scheme   string
	Username string
	// Secrets holds what flags, the environment or .env supplied, by scheme
	// name. The entry for the resolved scheme wins over the store.
	Secrets map[string]string
	// BaseURL is the service URL. Without it the store is not consulted.
	BaseURL string
	// LookupURL returns the URL the stored secret is keyed by when that is not
	// BaseURL. It runs only when the store is consulted.
	LookupURL func() (string, error)
	// MissingSteps, when set, replace the product's MissingStep for this call.
	MissingSteps []string
}

// Resolve produces a validated Credential. A secret supplied for the scheme
// takes precedence; otherwise it is loaded from the store. A scheme of Kind
// None resolves without touching the store.
func Resolve(req Request, store *credstore.Store) (Credential, error) {
	scheme := req.Scheme
	if scheme == "" {
		scheme = product().DefaultScheme
	}
	cred := Credential{Scheme: scheme, Username: req.Username, Secret: req.Secrets[scheme]}
	if !cred.NeedsSecret() {
		return cred, cred.Validate()
	}

	if cred.Secret == "" && store != nil && req.BaseURL != "" {
		lookupBase := req.BaseURL
		if req.LookupURL != nil {
			var err error
			if lookupBase, err = req.LookupURL(); err != nil {
				return Credential{}, err
			}
		}
		loaded, err := store.Load(AccountKey(lookupBase, scheme))
		switch {
		case err == nil:
			cred.Secret = loaded
		case errors.Is(err, credstore.ErrSecretNotFound):
			return Credential{}, missingError(req.MissingSteps)
		default:
			return Credential{}, inaccessibleError(err)
		}
	}

	if err := cred.Validate(); err != nil {
		return Credential{}, err
	}
	return cred, nil
}

// Save stores a credential's secret for later resolution and returns the
// backend that accepted it: "keychain", "file", or BackendNone for a scheme
// with no secret.
func Save(baseURL string, cred Credential, store *credstore.Store) (string, error) {
	if err := cred.Validate(); err != nil {
		return "", err
	}
	if !cred.NeedsSecret() {
		return BackendNone, nil
	}
	return store.Save(AccountKey(baseURL, cred.Scheme), cred.Secret)
}

// Forget removes any stored secret for the base URL and scheme.
func Forget(baseURL, scheme string, store *credstore.Store) error {
	return store.Delete(AccountKey(baseURL, scheme))
}

const hostRetryStep = "Retry the same command with access to the host user environment (home directory and OS keychain)."

// HostRecovery is the machine-readable recovery for a credential the current
// sandbox cannot see: retry the same command where the user's home directory
// and keychain are reachable.
func HostRecovery() cerrors.Recovery {
	return cerrors.Recovery{
		Action:   "retry_current_command",
		Scope:    "host",
		Requires: []string{"user_home", "os_keychain"},
	}
}

func missingError(last []string) *cerrors.CLIError {
	p := product()
	if last == nil {
		last = nonEmpty(p.MissingStep)
	}
	return cerrors.New(cerrors.CategoryConfig, "CREDENTIAL_NOT_VISIBLE_OR_MISSING",
		"stored "+p.Name+" credentials are missing or not visible in this execution environment").
		WithHint("An agent sandbox may have a different home or keychain view even when the host is already configured.").
		WithNextSteps(append(append([]string{hostRetryStep}, nonEmpty(p.Doctor)...), last...)...).
		WithRecovery(HostRecovery())
}

func inaccessibleError(err error) *cerrors.CLIError {
	p := product()
	return cerrors.Wrap(err, cerrors.CategoryConfig, "CREDENTIAL_STORE_INACCESSIBLE",
		"stored "+p.Name+" credentials cannot be read in this execution environment").
		WithHint("The configured credential store is inaccessible; this commonly happens when an agent sandbox cannot access the host keychain or credential file.").
		WithNextSteps(append(append([]string{hostRetryStep}, nonEmpty(p.Doctor)...), nonEmpty(p.InaccessibleStep)...)...).
		WithRecovery(HostRecovery())
}

// ServiceMismatchError reports that the stored credential belongs to another
// service URL than the one in effect. CLIs return it from their LookupURL.
func ServiceMismatchError() *cerrors.CLIError {
	p := product()
	e := cerrors.New(cerrors.CategoryConfig, "CREDENTIAL_SERVICE_MISMATCH",
		"stored credential lookup does not match the complete service URL")
	if p.MismatchHint != "" {
		e.WithHint(p.MismatchHint)
	}
	if len(p.MismatchSteps) > 0 {
		e.WithNextSteps(p.MismatchSteps...)
	}
	return e
}

func nonEmpty(s string) []string {
	if s == "" {
		return nil
	}
	return []string{s}
}
