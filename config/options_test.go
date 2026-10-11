package config

import (
	"reflect"
	"testing"
	"time"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

func TestEncodeDecode(t *testing.T) {
	useTool(t, nil)
	in := toolConfig{BaseURL: "https://x", Auth: toolAuth{Scheme: "pat"},
		Defaults: toolDefaults{PageSize: -1, Timeout: 90 * time.Second, MaxRetries: 0, ReadOnly: true}}
	got := Encode(in)
	want := Values{KeyServer: "https://x", KeyAuthScheme: "pat", "defaults.timeout": "1m30s", "defaults.read_only": "true"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("Encode = %v, want %v (zero and negative numbers are unset)", got, want)
	}

	var out toolConfig
	Decode(Values{"defaults.page_size": "x", "defaults.timeout": "soon", "defaults.max_retries": "7", "defaults.read_only": "bogus"}, &out)
	if out.Defaults.PageSize != 25 || out.Defaults.Timeout != 30*time.Second || out.Defaults.MaxRetries != 7 || out.Defaults.ReadOnly {
		t.Errorf("bad values must take the field default: %+v", out.Defaults)
	}
	for _, s := range []string{"1", "true", "YES", " on "} {
		Decode(Values{"defaults.read_only": s}, &out)
		if !out.Defaults.ReadOnly {
			t.Errorf("%q should decode as true", s)
		}
	}
	for _, s := range []string{"0", "false", "No", "OFF"} {
		out.Defaults.ReadOnly = true
		Decode(Values{"defaults.read_only": s}, &out)
		if out.Defaults.ReadOnly {
			t.Errorf("%q should decode as false", s)
		}
	}
	Decode(Values{"defaults.max_retries": "-4", "defaults.timeout": "0"}, &out)
	if out.Defaults.MaxRetries != -4 || out.Defaults.Timeout != 0 {
		t.Errorf("explicit values must be kept: %+v", out.Defaults)
	}

	defer func() {
		if recover() == nil {
			t.Error("an unsupported field type must panic")
		}
	}()
	Encode(struct {
		F float64 `config:"f"`
	}{1})
}

func TestStoredContextIgnoresEnvironment(t *testing.T) {
	useTool(t, nil)
	t.Setenv("TOOL_USERNAME", "env-user")
	t.Setenv("TOOL_SERVER", "https://env.example.com")
	v := StoredContext(toolContext{Name: "a", BaseURL: "https://site.cloud.example"}, toolDefaults{Format: "table"})
	if v[KeyServer] != "https://site.cloud.example" || v["auth.username"] != "" || v["defaults.format"] != "table" {
		t.Errorf("StoredContext = %v", v)
	}
	if v[KeyAuthScheme] != "basic" {
		t.Errorf("AfterMerge must run: scheme = %q", v[KeyAuthScheme])
	}
}

func TestAfterMergeOnlyReplacesTheDefault(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir), Flags: toolFlags{BaseURL: "https://site.cloud.example"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Auth.Scheme != "basic" {
		t.Errorf("default scheme on a hosted site = %q, want basic", got.Config.Auth.Scheme)
	}
	got, err = loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir), Flags: toolFlags{BaseURL: "https://site.cloud.example", AuthScheme: "pat"}})
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Auth.Scheme != "pat" {
		t.Errorf("an explicit scheme must survive AfterMerge, got %q", got.Config.Auth.Scheme)
	}
}

func TestResetBy(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	writeFile(t, FilePath(dir), "contexts:\n  - name: a\n    server: https://Host.example.com:443/\n    flavor: auto\n    detected_flavor: cloud\n")
	load := func(flags toolFlags) *toolResolved {
		t.Helper()
		got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir), Flags: flags})
		if err != nil {
			t.Fatal(err)
		}
		return got
	}
	if got := load(toolFlags{}); got.Config.DetectedFlavor != flavorCloud || got.Sources["detected_flavor"] != "file" {
		t.Errorf("stored server keeps the detected flavor: %+v", got.Config)
	}
	if got := load(toolFlags{BaseURL: "https://host.example.com"}); got.Config.DetectedFlavor != flavorCloud {
		t.Errorf("an equivalent spelling keeps the detected flavor: %+v", got.Config)
	}
	got := load(toolFlags{BaseURL: "https://other.example.com"})
	if got.Config.DetectedFlavor != "" {
		t.Errorf("another server drops the detected flavor: %+v", got.Config)
	}
	if _, ok := got.Sources["detected_flavor"]; ok {
		t.Error("a dropped value has no source")
	}
	if got := load(toolFlags{Flavor: flavorSelf}); got.Config.DetectedFlavor != flavorCloud {
		t.Errorf("Load drops only on a server change: %+v", got.Config)
	}

	file, _, err := readTool(dir)
	if err != nil {
		t.Fatal(err)
	}
	plan := func(flags toolFlags) toolContext {
		t.Helper()
		r, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir), Flags: flags, Context: "a", Setup: true})
		if err != nil {
			t.Fatal(err)
		}
		p, err := planTool(file, "a", r, true, false)
		if err != nil {
			t.Fatal(err)
		}
		return p.File.Contexts[0]
	}
	if c := plan(toolFlags{AuthScheme: "basic"}); c.DetectedFlavor != flavorCloud {
		t.Errorf("an unrelated change keeps the detected flavor: %+v", c)
	}
	if c := plan(toolFlags{BaseURL: "HTTPS://HOST.example.com", AuthScheme: "basic"}); c.DetectedFlavor != flavorCloud || c.BaseURL != "https://Host.example.com:443/" {
		t.Errorf("a respelled server is no change: %+v", c)
	}
	if c := plan(toolFlags{BaseURL: "https://other.example.com", AuthScheme: "basic"}); c.DetectedFlavor != "" {
		t.Errorf("a server change clears the detected flavor: %+v", c)
	}
	if c := plan(toolFlags{Flavor: flavorSelf}); c.DetectedFlavor != "" || c.Flavor != flavorSelf {
		t.Errorf("a flavor change clears the detected flavor: %+v", c)
	}
}

func TestStoredServerKeepsCredentialLookup(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	writeFile(t, FilePath(dir), "contexts:\n  - name: a\n    server: https://Host.example.com:443/\n")
	for _, tc := range []struct{ flag, want string }{
		{"", ""},
		{"https://host.example.com", "https://Host.example.com:443/"},
		{"https://other.example.com", ""},
		{"not a url", ""},
	} {
		got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir), Flags: toolFlags{BaseURL: tc.flag}})
		if err != nil {
			t.Fatal(err)
		}
		if got.CredentialBaseURL != tc.want {
			t.Errorf("--base-url %q: StoredServer = %q, want %q", tc.flag, got.CredentialBaseURL, tc.want)
		}
	}
}

func TestValidateService(t *testing.T) {
	useTool(t, nil)
	base := Values{KeyServer: "https://s.example.com", KeyAuthScheme: "pat"}
	with := func(k, v string) Values {
		out := Values{}
		for key, val := range base {
			out[key] = val
		}
		out[k] = v
		return out
	}
	for _, tc := range []struct {
		v    Values
		code string
	}{
		{base, ""},
		{with(KeyServer, ""), "NO_BASE_URL"},
		{with(KeyServer, "s.example.com"), "BAD_BASE_URL"},
		{with(KeyCredentialURL, "/x"), "BAD_CREDENTIAL_URL"},
		{with(KeyAuthScheme, "bearer"), "AUTH_BAD_SCHEME"},
		{with("flavor", "mainframe"), "BAD_FLAVOR"},
		{with(KeyServer, "https://site.cloud.example"), "AUTH_CLOUD_NEEDS_BASIC"},
	} {
		err := ValidateService(tc.v)
		if got := cerrors.AsCLIError(err); (tc.code == "" && err != nil) || (tc.code != "" && (got == nil || got.Code != tc.code)) {
			t.Errorf("%v: got %v, want %q", tc.v, err, tc.code)
		}
	}
	if ce := cerrors.AsCLIError(ValidateService(with(KeyAuthScheme, ""))); ce.Hint != "Use one of: pat, basic." {
		t.Errorf("hint = %q", ce.Hint)
	}

	useTool(t, func(s *Schema) {
		s.Schemes = []string{"basic"}
		s.Words.SchemeHint = "Use: basic. Nothing else works here."
	})
	if ce := cerrors.AsCLIError(ValidateService(base)); ce.Code != "AUTH_BAD_SCHEME" || ce.Hint != "Use: basic. Nothing else works here." {
		t.Errorf("SchemeHint not used: %+v", ce)
	}
}

func TestURLRules(t *testing.T) {
	useTool(t, func(s *Schema) { s.URL = URLRules{AssumeScheme: "http", TrimSuffix: "/api/v1"} })
	for in, want := range map[string]string{
		"host.example.com:9090":                 "http://host.example.com:9090",
		"host.example.com/api/v1/":              "http://host.example.com",
		"https://Host.example.com:443/p/api/v1": "https://host.example.com/p",
		"http://[::1]:80/":                      "http://[::1]",
		"https://h.example.com/api/v1/x":        "https://h.example.com/api/v1/x",
		// Trimming rewrites the path, so an escaped slash comes back decoded,
		// as it always has for the CLI that trims.
		"https://h.example.com/deploy%2Fpath/":    "https://h.example.com/deploy/path",
		"h.example.com/prefix/api/v1/api/v1/":     "http://h.example.com/prefix/api/v1",
		"  HTTPS://H.EXAMPLE.COM:8443/Case/Path ": "https://h.example.com:8443/Case/Path",
	} {
		got, err := NormalizeServiceURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeServiceURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}

	useTool(t, func(s *Schema) { s.URL = URLRules{AssumeScheme: "http"} })
	for in, want := range map[string]string{
		"localhost":                            "http://localhost",
		"https://h.example.com/api/v1/":        "https://h.example.com/api/v1",
		"https://h.example.com/deploy%2Fpath/": "https://h.example.com/deploy%2Fpath",
	} {
		got, err := NormalizeServiceURL(in)
		if err != nil || got != want {
			t.Errorf("NormalizeServiceURL(%q) = %q, %v; want %q", in, got, err, want)
		}
	}
}

func TestCheckPlanRunsBeforeTheConflict(t *testing.T) {
	useTool(t, func(s *Schema) {
		s.CheckPlan = func(old, next Values, exists bool) error {
			if exists && old[KeyAuthScheme] == "basic" && next[KeyServer] != old[KeyServer] {
				return cerrors.New(cerrors.CategoryConfig, "PINNED", "this context's server is pinned")
			}
			return nil
		}
	})
	file := toolFile{Contexts: []toolContext{{Name: "a", BaseURL: "https://a.example.com", Auth: toolAuth{Scheme: "basic"}}}}
	r := &toolResolved{Config: toolConfig{BaseURL: "https://b.example.com", Auth: toolAuth{Scheme: "basic"}},
		Resolved: &Resolved{Sources: map[string]string{KeyServer: "flag"}}}
	if _, err := planTool(file, "a", r, false, false); cerrors.AsCLIError(err).Code != "PINNED" {
		t.Errorf("CheckPlan must beat the conflict: %v", err)
	}
	if _, err := planTool(file, "new", r, false, false); err != nil {
		t.Errorf("a new context passes: %v", err)
	}
}

func TestPlanServiceContextNamesAndErrors(t *testing.T) {
	useTool(t, nil)
	r := &toolResolved{Config: toolConfig{BaseURL: "https://s.example.com/", Auth: toolAuth{Scheme: "pat"}}, Resolved: &Resolved{}}
	p, err := planTool(toolFile{Contexts: []toolContext{{Name: "Legacy"}}}, "  LEGACY ", r, true, false)
	if err != nil {
		t.Fatal(err)
	}
	if p.Context != "Legacy" || len(p.File.Contexts) != 1 || p.File.Contexts[0].BaseURL != "https://s.example.com" {
		t.Errorf("an existing context keeps its stored name: %+v", p)
	}
	p, err = planTool(toolFile{}, "  New Team ", r, false, false)
	if err != nil || p.Context != "new team" || p.File.CurrentContext != "new team" {
		t.Errorf("a new name is normalized: %+v %v", p, err)
	}
	for _, name := range []string{"", "  ", "a\tb", "a\nb"} {
		if _, err := planTool(toolFile{}, name, r, false, false); cerrors.AsCLIError(err).Code != "CTX_NAME_EMPTY" {
			t.Errorf("%q: %v", name, err)
		}
	}
	r.Config.BaseURL = ""
	if _, err := planTool(toolFile{}, "x", r, false, false); cerrors.AsCLIError(err).Code != "NO_BASE_URL" {
		t.Errorf("missing server: %v", err)
	}
}

func TestWithCredentialGuide(t *testing.T) {
	useTool(t, nil)
	guide := func(url string) func() (AuthGuide, error) {
		return func() (AuthGuide, error) { return AuthGuide{CredentialURL: url}, nil }
	}
	for _, code := range []string{"AUTH_NO_TOKEN", "AUTH_NO_BASIC", "CREDENTIAL_NOT_VISIBLE_OR_MISSING"} {
		err := WithCredentialGuide(cerrors.New(cerrors.CategoryConfig, code, "m").WithNextSteps("first"), guide("https://p"))
		if got := cerrors.AsCLIError(err).NextSteps; !reflect.DeepEqual(got, []string{"first", "tool auth guide", "Credential page: https://p"}) {
			t.Errorf("%s: %v", code, got)
		}
	}
	err := WithCredentialGuide(cerrors.New(cerrors.CategoryConfig, "AUTH_NO_BASIC", "m").WithNextSteps(), guide(""))
	if got := cerrors.AsCLIError(err).NextSteps; !reflect.DeepEqual(got, []string{"tool auth guide"}) {
		t.Errorf("an empty credential page is left out: %v", got)
	}
	err = WithCredentialGuide(cerrors.New(cerrors.CategoryConfig, "CREDENTIAL_STORE_INACCESSIBLE", "m").WithNextSteps("host"), guide("https://p"))
	if got := cerrors.AsCLIError(err).NextSteps; !reflect.DeepEqual(got, []string{"host"}) {
		t.Errorf("other errors keep their steps: %v", got)
	}
	err = WithCredentialGuide(cerrors.New(cerrors.CategoryConfig, "AUTH_NO_TOKEN", "m").WithNextSteps("x"),
		func() (AuthGuide, error) { return AuthGuide{}, cerrors.New(cerrors.CategoryConfig, "BAD", "b") })
	if got := cerrors.AsCLIError(err).NextSteps; !reflect.DeepEqual(got, []string{"x"}) {
		t.Errorf("a failed guide adds nothing: %v", got)
	}
	if WithCredentialGuide(nil, guide("")) != nil {
		t.Error("nil stays nil")
	}
	if got := (AuthGuide{Server: "https://s", Scheme: "pat", Instructions: []string{"i"}}).Lines(); !reflect.DeepEqual(got, []string{"Service: https://s (pat)", "i"}) {
		t.Errorf("Lines = %v", got)
	}
}

func TestFileMethods(t *testing.T) {
	useTool(t, nil)
	f := toolFile{Contexts: []toolContext{{Name: "Alpha"}, {Name: "beta"}}}
	f.Upsert(toolContext{Name: "ALPHA", BaseURL: "https://a"})
	f.Upsert(toolContext{Name: "gamma"})
	if got := f.ContextNames(); !reflect.DeepEqual(got, []string{"ALPHA", "beta", "gamma"}) || f.Contexts[0].BaseURL != "https://a" {
		t.Errorf("Upsert: %v %+v", got, f.Contexts)
	}
	if !f.Remove("BETA") || f.Remove("beta") {
		t.Error("Remove reports whether it removed a context")
	}
	if got := f.ContextNames(); !reflect.DeepEqual(got, []string{"ALPHA", "gamma"}) {
		t.Errorf("Remove: %v", got)
	}
	if _, ok := f.Context("missing"); ok {
		t.Error("Context found a missing name")
	}
}

func TestUseAndDeleteContext(t *testing.T) {
	useTool(t, nil)
	f := toolFile{CurrentContext: "Beta", Contexts: []toolContext{{Name: "Alpha"}, {Name: "Beta"}, {Name: "gamma"}}}
	if name, err := UseContext(&f, "alpha"); err != nil || name != "Alpha" || f.CurrentContext != "Alpha" {
		t.Errorf("UseContext = %q, %v; current %q", name, err, f.CurrentContext)
	}
	if _, err := UseContext(&f, "ghost"); cerrors.AsCLIError(err).Code != "UNKNOWN_CONTEXT" {
		t.Errorf("UseContext unknown: %v", err)
	}
	gone, err := DeleteContext(&f, "ALPHA")
	if err != nil || gone.Name != "Alpha" || f.CurrentContext != "Beta" || !reflect.DeepEqual(f.ContextNames(), []string{"Beta", "gamma"}) {
		t.Errorf("deleting the current context: %+v %v %+v", gone, err, f)
	}
	if _, err := DeleteContext(&f, "gamma"); err != nil || f.CurrentContext != "Beta" {
		t.Errorf("deleting another context keeps current: %v %+v", err, f)
	}
	if _, err := DeleteContext(&f, "beta"); cerrors.AsCLIError(err).Code != "LAST_CONTEXT" {
		t.Errorf("the last context stays: %v", err)
	}
	if _, err := DeleteContext(&f, "ghost"); cerrors.AsCLIError(err).Code != "UNKNOWN_CONTEXT" {
		t.Errorf("unknown: %v", err)
	}
}

func TestNormalizeNames(t *testing.T) {
	for in, want := range map[string]string{" Prod ": "prod", "Ops Team": "ops team", "": ""} {
		if got := NormalizeContextName(in); got != want {
			t.Errorf("NormalizeContextName(%q) = %q", in, got)
		}
	}
	for in, want := range map[string]string{" Alice@Example.COM ": "alice@example.com", " DOMAIN\\Bob ": "DOMAIN\\Bob", "CaseSensitive": "CaseSensitive"} {
		if got := NormalizeUsername(in); got != want {
			t.Errorf("NormalizeUsername(%q) = %q", in, got)
		}
	}
	if got := QuoteArg("it's"); got != `'it'"'"'s'` {
		t.Errorf("QuoteArg = %s", got)
	}
}
