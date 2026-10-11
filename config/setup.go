package config

import (
	"fmt"
	"maps"
	"net/url"
	"reflect"
	"slices"
	"strings"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

// NormalizeServiceURL returns the canonical spelling of a service URL: lower
// case scheme and host, no default port, no trailing slash, the deployment
// path kept. It rejects URLs that carry credentials, a query or a fragment.
// It is for setup and comparison, never for deriving an API route.
func NormalizeServiceURL(raw string) (string, error) {
	rules := schema().URL
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", cerrors.New(cerrors.CategoryConfig, "NO_BASE_URL", "no service URL configured").
			WithNextSteps(step(schema().Words.SetContext + " <name> --base-url <url>"))
	}
	if rules.AssumeScheme != "" && !strings.Contains(s, "://") {
		s = rules.AssumeScheme + "://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" || strings.ContainsAny(s, "\r\n\t") {
		return "", cerrors.New(cerrors.CategoryConfig, "BAD_BASE_URL",
			"service URL must be an HTTP(S) URL without credentials, query or fragment")
	}
	u.Scheme = strings.ToLower(u.Scheme)
	u.Host = strings.ToLower(u.Host)
	if (u.Scheme == "https" && u.Port() == "443") || (u.Scheme == "http" && u.Port() == "80") {
		host := u.Hostname()
		if strings.Contains(host, ":") {
			host = "[" + host + "]"
		}
		u.Host = host
	}
	if rules.TrimSuffix != "" {
		u.Path = strings.TrimSuffix(strings.TrimRight(u.Path, "/"), rules.TrimSuffix)
	}
	return strings.TrimRight(u.String(), "/"), nil
}

func validateCredentialURL(raw string) error {
	if raw == "" {
		return nil
	}
	u, err := url.Parse(raw)
	if err != nil || u.Hostname() == "" || (u.Scheme != "https" && u.Scheme != "http") ||
		u.User != nil || strings.ContainsAny(raw, "\r\n\t") {
		return cerrors.New(cerrors.CategoryConfig, "BAD_CREDENTIAL_URL",
			"credential URL must be an absolute HTTP(S) page URL without embedded credentials")
	}
	return nil
}

// ValidateService checks the public setup fields in v: the service URL, the
// credential page, the scheme, then the schema's Validate hook. It never
// contacts a server.
func ValidateService(v Values) error {
	if _, err := NormalizeServiceURL(v[KeyServer]); err != nil {
		return err
	}
	if err := validateCredentialURL(v[KeyCredentialURL]); err != nil {
		return err
	}
	s := schema()
	if !slices.Contains(s.Schemes, v[KeyAuthScheme]) {
		hint := s.Words.SchemeHint
		if hint == "" {
			hint = "Use one of: " + strings.Join(s.Schemes, ", ") + "."
		}
		return cerrors.New(cerrors.CategoryConfig, "AUTH_BAD_SCHEME", "unsupported authentication scheme").WithHint(hint)
	}
	if s.Validate != nil {
		return s.Validate(v)
	}
	return nil
}

// FieldChange is one public setting before and after a change. Secrets never
// appear in one.
type FieldChange struct {
	Before string `json:"before"`
	After  string `json:"after"`
}

// ContextPlan is a planned change to one context, shared by preview and
// execution. File is the exact file to write.
type ContextPlan[C, D any] struct {
	Context        string                 `json:"context"`
	Changed        bool                   `json:"changed"`
	CurrentContext string                 `json:"current_context"`
	Changes        map[string]FieldChange `json:"changes"`
	NextSteps      []string               `json:"next_steps"`
	File           File[C, D]             `json:"-"`
}

// PlanServiceContext merges the Service fields of v into the context name
// (normalized; an existing context keeps its stored spelling) without side
// effects. sources says which layer supplied each value: an existing non-empty
// field changes only for a flag, the environment or .env, and such a change
// is a CONFIG_CONTEXT_CONFLICT unless overwrite is set. The first context in a
// file, or any context with activate, becomes current.
func PlanServiceContext[C, D any](file File[C, D], name string, v Values, sources map[string]string, overwrite, activate bool) (ContextPlan[C, D], error) {
	name = NormalizeContextName(name)
	if name == "" || strings.ContainsAny(name, "\r\n\t") {
		return ContextPlan[C, D]{}, cerrors.New(cerrors.CategoryUsage, "CTX_NAME_EMPTY", "provide a non-empty context name")
	}
	old, exists := file.Context(name)
	if exists {
		name = nameOf(old)
	}
	normalized, err := NormalizeServiceURL(v[KeyServer])
	if err != nil {
		return ContextPlan[C, D]{}, err
	}
	v = maps.Clone(v)
	v[KeyServer] = normalized
	if err := ValidateService(v); err != nil {
		return ContextPlan[C, D]{}, err
	}

	next := old
	nv := reflect.ValueOf(&next).Elem()
	set(nv, KeyName, name)
	changes := map[string]FieldChange{}
	conflicts := map[string]FieldChange{}
	fields := schema().Fields
	for _, f := range fields {
		if !f.Service || !has(nv.Type(), f.Key) {
			continue
		}
		value, before := v[f.Key], get(nv, f.Key)
		switch source := sources[f.Key]; {
		case exists && before != "" && source != "flag" && source != "env" && source != "dotenv":
			continue
		case f.Key == KeyServer && before != "":
			if normal, e := NormalizeServiceURL(before); e == nil && normal == value {
				continue
			}
		}
		if before == value {
			continue
		}
		change := FieldChange{Before: before, After: value}
		changes[f.Key] = change
		if exists && before != "" {
			conflicts[f.Key] = change
		}
		set(nv, f.Key, value)
	}
	ov := reflect.ValueOf(&old).Elem()
	for _, f := range fields {
		for _, k := range f.ResetBy {
			if get(nv, k) != get(ov, k) {
				set(nv, f.Key, "")
				break
			}
		}
	}
	if check := schema().CheckPlan; check != nil {
		if err := check(Encode(&old), Encode(&next), exists); err != nil {
			return ContextPlan[C, D]{}, err
		}
	}
	if len(conflicts) > 0 && !overwrite {
		return ContextPlan[C, D]{}, cerrors.New(cerrors.CategoryConflict, "CONFIG_CONTEXT_CONFLICT",
			"team presets conflict with the existing context").
			WithDetails(conflicts).
			WithHint("Inspect the field differences; use --overwrite to update the supplied service fields, or choose another context name.").
			WithNextSteps(step(schema().Words.SetContext + " --help"))
	}

	result := file
	result.Contexts = append([]C(nil), file.Contexts...)
	replaced := false
	for i, c := range result.Contexts {
		if nameOf(c) == name {
			result.Contexts[i] = next
			replaced = true
			break
		}
	}
	if !replaced {
		result.Contexts = append(result.Contexts, next)
	}
	if len(file.Contexts) == 0 || activate {
		result.CurrentContext = name
	}
	if result.CurrentContext != file.CurrentContext {
		changes["current_context"] = FieldChange{Before: file.CurrentContext, After: result.CurrentContext}
	}
	w := schema().Words
	selected := w.App + " " + w.ContextFlag + " " + QuoteArg(name) + " "
	return ContextPlan[C, D]{
		Context:        name,
		Changed:        !reflect.DeepEqual(file, result),
		CurrentContext: result.CurrentContext,
		Changes:        changes,
		NextSteps:      []string{selected + w.AuthGuide, selected + w.AuthLogin},
		File:           result,
	}, nil
}

// AuthGuide is offline guidance on where to get a credential. It is never a
// capability or authentication probe, and no request is sent to
// CredentialURL. Each CLI fills it with its own content.
type AuthGuide struct {
	Server           string   `json:"server"`
	Flavor           string   `json:"flavor,omitempty"`
	Scheme           string   `json:"scheme"`
	CredentialURL    string   `json:"credential_url"`
	Source           string   `json:"source"`
	Instructions     []string `json:"instructions"`
	DocumentationURL string   `json:"documentation_url"`
	NextSteps        []string `json:"next_steps"`
}

// Lines renders the guide for plain prompts and terminal forms. The
// credential page line is left out when there is none.
func (g AuthGuide) Lines() []string {
	lines := []string{fmt.Sprintf("Service: %s (%s)", g.Server, g.Scheme)}
	if g.CredentialURL != "" {
		lines = append(lines, "Credential page: "+g.CredentialURL)
	}
	return append(lines, g.Instructions...)
}

// WithCredentialGuide appends the guide steps to a missing-credential error
// (AUTH_NO_TOKEN, AUTH_NO_BASIC or CREDENTIAL_NOT_VISIBLE_OR_MISSING) when
// guide succeeds. Other errors, such as an inaccessible store, keep their
// host recovery steps alone.
func WithCredentialGuide(err error, guide func() (AuthGuide, error)) error {
	if err == nil {
		return nil
	}
	ce := cerrors.AsCLIError(err)
	switch ce.Code {
	case "AUTH_NO_TOKEN", "AUTH_NO_BASIC", "CREDENTIAL_NOT_VISIBLE_OR_MISSING":
		if g, e := guide(); e == nil {
			ce.NextSteps = append(ce.NextSteps, step(schema().Words.AuthGuide))
			if g.CredentialURL != "" {
				ce.NextSteps = append(ce.NextSteps, "Credential page: "+g.CredentialURL)
			}
		}
	}
	return ce
}
