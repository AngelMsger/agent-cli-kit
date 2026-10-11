// Package config resolves a CLI's settings from layered sources (flags, the
// environment, a .env file, a YAML file of named contexts, built-in defaults),
// records the layer that supplied each value, and reads and writes the file.
//
// Nothing here names a product. A CLI calls Register once at start-up with its
// field table, file location and command wording, the same way it installs its
// error hints with errors.SetGuidance. The CLI keeps its own typed structs and
// the YAML shape of its file; struct fields bind to the field table through
// `config:"<key>"` tags, and Encode and Decode move values between the two.
package config

import "sync/atomic"

// Keys the kit itself reads: the context name, the service URL, the auth
// scheme and the credential page. Every CLI in the family uses them.
const (
	KeyName          = "name"
	KeyServer        = "server"
	KeyAuthScheme    = "auth.scheme"
	KeyCredentialURL = "auth.credential_url"
)

// Field declares one setting.
type Field struct {
	// Key names the field in provenance, in struct tags and in
	// ContextPlan.Changes, such as "server" or "defaults.timeout".
	Key string
	// Env lists the environment and .env names that supply the field, highest
	// precedence first.
	Env []string
	// Default is the built-in value, the lowest layer. Decode also falls back
	// to it when a number, duration or boolean does not parse.
	Default string
	// Service marks a team preset. A Setup load keeps only Service fields from
	// the environment and .env, and PlanServiceContext merges the Service
	// fields that live in a context.
	Service bool
	// Implies is the auth scheme this field (a secret) selects when the
	// environment or .env supplies it. An auth scheme set in the same layer
	// still wins, and among secrets the first in table order decides.
	Implies string
	// ResetBy lists keys whose change invalidates this cached value, such as
	// a detected server flavor. PlanServiceContext clears it when one of them
	// changes, and Load drops it when ResetBy names KeyServer and the server
	// in effect is not the stored one.
	ResetBy []string
}

// Words is the product wording of the steps and hints the package prints.
// Commands are given without the binary; the package prefixes App.
type Words struct {
	App          string // the binary, such as "tool"
	ContextFlag  string // the flag that picks a context, such as "--use-context"
	ContextEnv   string // the variable that picks a context, such as "TOOL_CONTEXT"
	NoHintEnv    string // the variable that silences the context notice
	ListContexts string // such as "config get-contexts"
	UseContext   string // such as "config use-context"
	SetContext   string // such as "config set-context"
	AuthGuide    string // such as "auth guide"
	AuthLogin    string // such as "auth login"
	// SchemeHint, when set, replaces the "Use one of: <Schemes>." hint on
	// AUTH_BAD_SCHEME.
	SchemeHint string
}

// Paths locates the config file at ~/<Dir>/<File>.
type Paths struct {
	Dir  string // relative to the home directory, such as ".vendor/tool"
	File string // such as "config.yaml"
	// Legacy, when set, is an older directory relative to the home directory.
	// ResolveDir uses it while only it holds the file.
	Legacy string
}

// URLRules adjusts NormalizeServiceURL.
type URLRules struct {
	// AssumeScheme is prefixed when the URL has no "://", such as "http".
	// Without it a bare host is BAD_BASE_URL.
	AssumeScheme string
	// TrimSuffix is removed from the end of the path, such as an API root
	// pasted in place of the server root.
	TrimSuffix string
}

// Schema is what a CLI registers. Everything beyond Fields, Paths, Words and
// Schemes is opt-in; README.md lists which CLI needs each option.
type Schema struct {
	Fields []Field
	Paths  Paths
	Words  Words
	// Schemes lists the accepted auth schemes, in the order the hint names
	// them.
	Schemes []string
	URL     URLRules
	// AfterMerge adjusts the merged values and their sources, for example to
	// pick a default scheme from other settings. Load and StoredContext run
	// it.
	AfterMerge func(v Values, sources map[string]string)
	// Validate adds product checks to ValidateService. It runs after the URL,
	// credential URL and scheme checks.
	Validate func(v Values) error
	// CheckPlan vets a planned context before PlanServiceContext reports a
	// conflict. old and next are the stored and planned contexts; exists is
	// false for a new one.
	CheckPlan func(old, next Values, exists bool) error
}

// Values holds settings by key. Empty strings mean unset.
type Values map[string]string

var registered atomic.Pointer[Schema]

// Register installs the CLI's schema. Call it once during start-up, before
// anything is loaded. A nil s restores the empty registration.
func Register(s *Schema) {
	if s == nil {
		registered.Store(nil)
		return
	}
	c := *s
	c.Fields = append([]Field(nil), s.Fields...)
	c.Schemes = append([]string(nil), s.Schemes...)
	registered.Store(&c)
}

func schema() *Schema {
	if s := registered.Load(); s != nil {
		return s
	}
	return &Schema{}
}

func field(key string) (Field, bool) {
	for _, f := range schema().Fields {
		if f.Key == key {
			return f, true
		}
	}
	return Field{}, false
}

// step renders a command as the user types it.
func step(command string) string { return schema().Words.App + " " + command }

// ListContextsStep is the command that lists contexts, as a next step.
func ListContextsStep() string { return step(schema().Words.ListContexts) }
