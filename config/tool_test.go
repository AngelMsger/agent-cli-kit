package config

import (
	"net/url"
	"path/filepath"
	"strings"
	"testing"
	"time"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

// This file declares "tool", a CLI shaped like the family's richest member:
// a detected flavor that a server change invalidates, a default scheme that
// depends on the flavor, an alias pair for one secret, a project default
// written first, and a legacy flat file. The tests drive the package through
// it the way a CLI does.

const (
	flavorAuto  = "auto"
	flavorCloud = "cloud"
	flavorSelf  = "selfhosted"
)

type toolAuth struct {
	CredentialURL string `config:"auth.credential_url"`
	Scheme        string `config:"auth.scheme"`
	Username      string `config:"auth.username"`
}

type toolContext struct {
	Name           string `config:"name"`
	BaseURL        string `config:"server"`
	Flavor         string `config:"flavor"`
	DetectedFlavor string `config:"detected_flavor"`
	Auth           toolAuth
}

type toolDefaults struct {
	Project    string        `config:"defaults.project"`
	Format     string        `config:"defaults.format"`
	PageSize   int           `config:"defaults.page_size"`
	Timeout    time.Duration `config:"defaults.timeout"`
	MaxRetries int           `config:"defaults.max_retries"`
	ReadOnly   bool          `config:"defaults.read_only"`
}

type toolConfig struct {
	BaseURL        string `config:"server"`
	Flavor         string `config:"flavor"`
	DetectedFlavor string `config:"detected_flavor"`
	Auth           toolAuth
	Defaults       toolDefaults
}

type toolSecrets struct {
	PAT      string `config:"secret.pat"`
	Password string `config:"secret.password"`
	APIToken string `config:"secret.api_token"`
}

type toolFlags struct {
	AuthScheme    string `config:"auth.scheme"`
	CredentialURL string `config:"auth.credential_url"`
	BaseURL       string `config:"server"`
	Flavor        string `config:"flavor"`
	Format        string `config:"defaults.format"`
	Timeout       string `config:"defaults.timeout"`
}

type toolFile = File[toolContext, toolDefaults]

// toolSchema is the registration a CLI would make in init().
func toolSchema() *Schema {
	return &Schema{
		Fields: []Field{
			{Key: KeyServer, Env: []string{"TOOL_SERVER"}, Service: true},
			{Key: KeyCredentialURL, Env: []string{"TOOL_CREDENTIAL_URL"}, Service: true},
			{Key: KeyAuthScheme, Env: []string{"TOOL_AUTH_SCHEME"}, Default: "pat", Service: true},
			{Key: "auth.username", Env: []string{"TOOL_USERNAME"}},
			{Key: "flavor", Env: []string{"TOOL_FLAVOR"}, Default: flavorAuto, Service: true},
			{Key: "detected_flavor", ResetBy: []string{KeyServer, "flavor"}},
			{Key: "defaults.project", Env: []string{"TOOL_DEFAULT_PROJECT"}},
			{Key: "defaults.format", Env: []string{"TOOL_FORMAT"}, Default: "json", Service: true},
			{Key: "defaults.page_size", Default: "25"},
			{Key: "defaults.timeout", Default: "30s"},
			{Key: "defaults.max_retries", Default: "3"},
			{Key: "defaults.read_only", Env: []string{"TOOL_CLI_READ_ONLY"}, Service: true},
			{Key: "secret.pat", Env: []string{"TOOL_PERSONAL_ACCESS_TOKEN", "TOOL_TOKEN"}, Implies: "pat"},
			{Key: "secret.password", Env: []string{"TOOL_PASSWORD"}, Implies: "basic"},
			{Key: "secret.api_token", Env: []string{"TOOL_API_TOKEN"}, Implies: "basic"},
		},
		Paths: Paths{Dir: filepath.Join(".vendor", "tool"), File: "config.yaml"},
		Words: Words{
			App:          "tool",
			ContextFlag:  "--use-context",
			ContextEnv:   "TOOL_CONTEXT",
			NoHintEnv:    "TOOL_CLI_NO_CONTEXT_HINT",
			ListContexts: "config get-contexts",
			UseContext:   "config use-context",
			SetContext:   "config set-context",
			AuthGuide:    "auth guide",
			AuthLogin:    "auth login",
		},
		Schemes: []string{"pat", "basic"},
		AfterMerge: func(v Values, sources map[string]string) {
			if sources[KeyAuthScheme] == "default" && toolFlavor(v) == flavorCloud {
				v[KeyAuthScheme] = "basic"
			}
		},
		Validate: func(v Values) error {
			switch v["flavor"] {
			case "", flavorAuto, flavorCloud, flavorSelf:
			default:
				return cerrors.New(cerrors.CategoryConfig, "BAD_FLAVOR", "flavor must be auto, cloud or selfhosted")
			}
			if toolFlavor(v) == flavorCloud && v[KeyAuthScheme] == "pat" {
				return cerrors.New(cerrors.CategoryConfig, "AUTH_CLOUD_NEEDS_BASIC", "the hosted service requires basic authentication")
			}
			return nil
		},
	}
}

// toolFlavor is the CLI's own flavor rule: explicit, then detected, then a
// known hosted domain.
func toolFlavor(v Values) string {
	if f := v["flavor"]; f != "" && f != flavorAuto {
		return f
	}
	if d := v["detected_flavor"]; d != "" {
		return d
	}
	if u, err := url.Parse(v[KeyServer]); err == nil && strings.HasSuffix(strings.ToLower(u.Hostname()), ".cloud.example") {
		return flavorCloud
	}
	return flavorAuto
}

// useTool registers the tool schema, adjusted by edit, for one test.
func useTool(t *testing.T, edit func(*Schema)) {
	t.Helper()
	s := toolSchema()
	if edit != nil {
		edit(s)
	}
	Register(s)
	t.Cleanup(func() { Register(nil) })
	for _, f := range s.Fields {
		for _, name := range f.Env {
			t.Setenv(name, "")
		}
	}
	t.Setenv("TOOL_CONTEXT", "")
}

// The tool's YAML shape: legacy flat fields at the root, then the contexts.
// The project default is written first, as the CLI always has.
type toolAuthShape struct {
	CredentialURL string `yaml:"credential_url,omitempty"`
	Scheme        string `yaml:"scheme,omitempty"`
	Username      string `yaml:"username,omitempty"`
}

type toolDefaultsShape struct {
	Project    string `yaml:"project,omitempty"`
	Format     string `yaml:"format,omitempty"`
	PageSize   int    `yaml:"page_size,omitempty"`
	Timeout    string `yaml:"timeout,omitempty"`
	MaxRetries int    `yaml:"max_retries,omitempty"`
	ReadOnly   bool   `yaml:"read_only,omitempty"`
}

type toolContextShape struct {
	Name           string        `yaml:"name"`
	Server         string        `yaml:"server,omitempty"`
	Flavor         string        `yaml:"flavor,omitempty"`
	DetectedFlavor string        `yaml:"detected_flavor,omitempty"`
	Auth           toolAuthShape `yaml:"auth,omitempty"`
}

type toolShape struct {
	Server         string             `yaml:"server,omitempty"`
	Flavor         string             `yaml:"flavor,omitempty"`
	DetectedFlavor string             `yaml:"detected_flavor,omitempty"`
	Auth           toolAuthShape      `yaml:"auth,omitempty"`
	CurrentContext string             `yaml:"current_context,omitempty"`
	Contexts       []toolContextShape `yaml:"contexts,omitempty"`
	Defaults       toolDefaultsShape  `yaml:"defaults,omitempty"`
}

func readTool(dir string) (toolFile, bool, error) {
	var s toolShape
	if ok, err := ReadFile(dir, &s); !ok || err != nil {
		return toolFile{}, false, err
	}
	timeout, _ := time.ParseDuration(s.Defaults.Timeout)
	f := toolFile{CurrentContext: s.CurrentContext, Defaults: toolDefaults{
		Project: s.Defaults.Project, Format: s.Defaults.Format, PageSize: s.Defaults.PageSize,
		Timeout: timeout, MaxRetries: s.Defaults.MaxRetries, ReadOnly: s.Defaults.ReadOnly,
	}}
	switch {
	case len(s.Contexts) > 0:
		for _, c := range s.Contexts {
			f.Contexts = append(f.Contexts, toolContext{Name: c.Name, BaseURL: c.Server, Flavor: c.Flavor,
				DetectedFlavor: c.DetectedFlavor, Auth: toolAuth(c.Auth)})
		}
	case s.Server != "":
		f.Contexts = []toolContext{{Name: DefaultContextName, BaseURL: s.Server, Flavor: s.Flavor,
			DetectedFlavor: s.DetectedFlavor, Auth: toolAuth(s.Auth)}}
		if f.CurrentContext == "" {
			f.CurrentContext = DefaultContextName
		}
	}
	return f, true, nil
}

func writeTool(dir string, f toolFile) error {
	s := toolShape{CurrentContext: f.CurrentContext, Defaults: toolDefaultsShape{
		Project: f.Defaults.Project, Format: f.Defaults.Format, PageSize: f.Defaults.PageSize,
		MaxRetries: f.Defaults.MaxRetries, ReadOnly: f.Defaults.ReadOnly,
	}}
	if f.Defaults.Timeout > 0 {
		s.Defaults.Timeout = f.Defaults.Timeout.String()
	}
	for _, c := range f.Contexts {
		s.Contexts = append(s.Contexts, toolContextShape{Name: c.Name, Server: c.BaseURL, Flavor: c.Flavor,
			DetectedFlavor: c.DetectedFlavor, Auth: toolAuthShape(c.Auth)})
	}
	return WriteFile(dir, &s)
}

type toolResolved struct {
	Config            toolConfig
	Secrets           toolSecrets
	CredentialBaseURL string
	*Resolved
}

type toolLoad struct {
	Setup      bool
	ConfigDir  string
	DotenvPath string
	Flags      toolFlags
	Context    string
}

// loadTool is the CLI's Load wrapper: read its file, load, decode.
func loadTool(opt toolLoad) (*toolResolved, error) {
	file, _, err := readTool(opt.ConfigDir)
	if err != nil {
		return nil, err
	}
	r, err := Load(file, LoadOptions{Setup: opt.Setup, DotenvPath: opt.DotenvPath, Flags: Encode(opt.Flags), Context: opt.Context})
	if err != nil {
		return nil, err
	}
	out := &toolResolved{Resolved: r, CredentialBaseURL: r.StoredServer}
	Decode(r.Values, &out.Config)
	Decode(r.Values, &out.Secrets)
	return out, nil
}

// planTool is the CLI's PlanServiceContext wrapper.
func planTool(file toolFile, name string, r *toolResolved, overwrite, activate bool) (ContextPlan[toolContext, toolDefaults], error) {
	return PlanServiceContext(file, name, Encode(r.Config), r.Sources, overwrite, activate)
}

// toolGuide is a CLI's Guide: product content around ValidateService.
func toolGuide(cfg toolConfig, sources map[string]string) (AuthGuide, error) {
	if err := ValidateService(Encode(cfg)); err != nil {
		return AuthGuide{}, err
	}
	base, _ := NormalizeServiceURL(cfg.BaseURL)
	g := AuthGuide{Server: base, Scheme: cfg.Auth.Scheme, CredentialURL: base, Source: "fallback",
		Flavor: toolFlavor(Encode(cfg)), DocumentationURL: "https://docs.example.test/tokens",
		Instructions: []string{"Create a token on your profile page."}, NextSteps: []string{"tool auth login"}}
	if cfg.Auth.CredentialURL != "" {
		g.CredentialURL = cfg.Auth.CredentialURL
		g.Source = sources[KeyCredentialURL]
		if g.Source == "" {
			g.Source = "config"
		}
	}
	return g, nil
}
