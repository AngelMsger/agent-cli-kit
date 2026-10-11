package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
}

func absent(dir string) string { return filepath.Join(dir, "absent.env") }

func TestLoadDefaults(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Flavor != flavorAuto {
		t.Errorf("Flavor = %q, want auto", got.Config.Flavor)
	}
	if got.Config.Defaults.Format != "json" {
		t.Errorf("Format = %q, want json", got.Config.Defaults.Format)
	}
	if got.Config.Defaults.PageSize != 25 || got.Config.Defaults.Timeout != 30*time.Second || got.Config.Defaults.MaxRetries != 3 {
		t.Errorf("Defaults = %+v", got.Config.Defaults)
	}
	if got.Sources[KeyAuthScheme] != "default" || got.ActiveContext != "" || got.ContextSource != ContextSourceNone {
		t.Errorf("sources=%v active=%q source=%q", got.Sources, got.ActiveContext, got.ContextSource)
	}
	for key, source := range got.Sources {
		if source == "default" && DefaultValues()[key] != got.Values[key] {
			t.Errorf("DefaultValues()[%q] = %q, Load gave %q", key, DefaultValues()[key], got.Values[key])
		}
	}
	if len(DefaultValues()) != len(got.Sources) {
		t.Errorf("DefaultValues() = %v, want the %d defaults Load used", DefaultValues(), len(got.Sources))
	}
}

func TestLoadFileLayer(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	writeFile(t, FilePath(dir), "server: https://file.example.com\nflavor: selfhosted\n")
	got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.BaseURL != "https://file.example.com" {
		t.Errorf("BaseURL = %q", got.Config.BaseURL)
	}
	if got.Sources[KeyServer] != "file" {
		t.Errorf("server source = %q, want file", got.Sources[KeyServer])
	}
}

func TestLoadPrecedenceEnvOverridesDotenvAndFile(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	writeFile(t, FilePath(dir), "server: https://file.example.com\n")
	dotenv := filepath.Join(dir, ".env")
	writeFile(t, dotenv, "TOOL_SERVER=https://dotenv.example.com\n")
	t.Setenv("TOOL_SERVER", "https://env.example.com")

	got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: dotenv})
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.BaseURL != "https://env.example.com" {
		t.Errorf("BaseURL = %q, want env value", got.Config.BaseURL)
	}
	if got.Sources[KeyServer] != "env" {
		t.Errorf("source = %q, want env", got.Sources[KeyServer])
	}
}

func TestLoadFlagWinsOverEverything(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	dotenv := filepath.Join(dir, ".env")
	writeFile(t, dotenv, "TOOL_SERVER=https://dotenv.example.com\n")
	t.Setenv("TOOL_SERVER", "https://env.example.com")

	got, err := loadTool(toolLoad{
		ConfigDir:  dir,
		DotenvPath: dotenv,
		Flags:      toolFlags{BaseURL: "https://flag.example.com"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.BaseURL != "https://flag.example.com" {
		t.Errorf("BaseURL = %q, want flag value", got.Config.BaseURL)
	}
	if got.Sources[KeyServer] != "flag" {
		t.Errorf("source = %q, want flag", got.Sources[KeyServer])
	}
}

func TestLoadDotenvOverridesFileButNotEnv(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	writeFile(t, FilePath(dir), "server: https://file.example.com\n")
	dotenv := filepath.Join(dir, ".env")
	writeFile(t, dotenv, "TOOL_SERVER=https://dotenv.example.com\n")

	got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: dotenv})
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.BaseURL != "https://dotenv.example.com" {
		t.Errorf("BaseURL = %q, want dotenv value", got.Config.BaseURL)
	}
}

func TestLoadDotenvErrors(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	if err := os.Mkdir(filepath.Join(dir, "dir.env"), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: filepath.Join(dir, "dir.env")}); err == nil {
		t.Error("an unreadable .env must fail the load")
	}
}

func TestLoadSecretFromEnvInfersScheme(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	t.Setenv("TOOL_PERSONAL_ACCESS_TOKEN", "tok-123")
	got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Secrets.PAT != "tok-123" {
		t.Errorf("PAT = %q", got.Secrets.PAT)
	}
	if got.Config.Auth.Scheme != "pat" || got.Sources[KeyAuthScheme] != "env" {
		t.Errorf("scheme = %q from %q, want pat from env", got.Config.Auth.Scheme, got.Sources[KeyAuthScheme])
	}
}

func TestLoadSchemeInference(t *testing.T) {
	cases := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"first secret in table order wins", map[string]string{"TOOL_PASSWORD": "p", "TOOL_TOKEN": "t"}, "pat"},
		{"password implies basic", map[string]string{"TOOL_PASSWORD": "p"}, "basic"},
		{"api token implies basic", map[string]string{"TOOL_API_TOKEN": "a"}, "basic"},
		{"explicit scheme wins", map[string]string{"TOOL_TOKEN": "t", "TOOL_AUTH_SCHEME": "basic"}, "basic"},
		{"empty secret ignored", map[string]string{"TOOL_TOKEN": ""}, "pat"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			useTool(t, nil)
			for k, v := range tc.env {
				t.Setenv(k, v)
			}
			dir := t.TempDir()
			got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
			if err != nil {
				t.Fatal(err)
			}
			if got.Config.Auth.Scheme != tc.want {
				t.Errorf("scheme = %q, want %q", got.Config.Auth.Scheme, tc.want)
			}
		})
	}
}

func TestLoadEnvAliasOrder(t *testing.T) {
	useTool(t, nil)
	t.Setenv("TOOL_PERSONAL_ACCESS_TOKEN", "long")
	t.Setenv("TOOL_TOKEN", "short")
	dir := t.TempDir()
	for i := 0; i < 20; i++ { // map order must never decide
		got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
		if err != nil {
			t.Fatal(err)
		}
		if got.Secrets.PAT != "long" {
			t.Fatalf("PAT = %q, want the first name's value", got.Secrets.PAT)
		}
	}
	t.Setenv("TOOL_PERSONAL_ACCESS_TOKEN", "")
	got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Secrets.PAT != "short" {
		t.Errorf("PAT = %q, want the alias's value", got.Secrets.PAT)
	}
}

func TestDotenvDoesNotMutateProcessEnv(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	dotenv := filepath.Join(dir, ".env")
	writeFile(t, dotenv, "TOOL_FORMAT=table\n")
	os.Unsetenv("TOOL_FORMAT")
	got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: dotenv})
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Defaults.Format != "table" || got.Sources["defaults.format"] != "dotenv" {
		t.Errorf("format = %q from %q", got.Config.Defaults.Format, got.Sources["defaults.format"])
	}
	if _, ok := os.LookupEnv("TOOL_FORMAT"); ok {
		t.Error(".env load must not set process environment variables")
	}
}

func TestWriteFileRoundTrip(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	in := toolFile{
		CurrentContext: "prod",
		Contexts: []toolContext{
			{Name: "default", BaseURL: "https://dc.example.com", Flavor: flavorSelf,
				Auth: toolAuth{Scheme: "pat"}},
			{Name: "prod", BaseURL: "https://site.cloud.example", Flavor: flavorCloud,
				DetectedFlavor: flavorCloud, Auth: toolAuth{Scheme: "basic", Username: "alice"}},
		},
		Defaults: toolDefaults{Format: "table", PageSize: 50, Timeout: 15 * time.Second, MaxRetries: 5},
	}
	if err := writeTool(dir, in); err != nil {
		t.Fatal(err)
	}
	got, exists, err := readTool(dir)
	if err != nil || !exists {
		t.Fatalf("ReadFile: exists=%v err=%v", exists, err)
	}
	if got.CurrentContext != "prod" || len(got.Contexts) != 2 {
		t.Fatalf("round trip mismatch: %+v", got)
	}
	prod, ok := got.Context("PROD")
	if !ok || prod.BaseURL != "https://site.cloud.example" || prod.Auth.Username != "alice" {
		t.Errorf("prod context = %+v", prod)
	}
	if got.Defaults.Timeout != 15*time.Second || got.Defaults.PageSize != 50 {
		t.Errorf("defaults = %+v", got.Defaults)
	}
}

func TestWriteFilePermissions(t *testing.T) {
	useTool(t, nil)
	dir := filepath.Join(t.TempDir(), "nested")
	if err := writeTool(dir, toolFile{Contexts: []toolContext{{Name: "default", BaseURL: "https://x"}}}); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(FilePath(dir))
	if err != nil {
		t.Fatal(err)
	}
	if perm := info.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o600 {
		t.Errorf("config file perm = %o, want 600", perm)
	}
	dinfo, err := os.Stat(dir)
	if err != nil {
		t.Fatal(err)
	}
	if perm := dinfo.Mode().Perm(); runtime.GOOS != "windows" && perm != 0o700 {
		t.Errorf("config dir perm = %o, want 700", perm)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 1 {
		t.Errorf("temporary file left behind: %v", entries)
	}
}

func TestReadFileMissingAndBroken(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	var s toolShape
	if ok, err := ReadFile(dir, &s); ok || err != nil {
		t.Fatalf("missing file: ok=%v err=%v", ok, err)
	}
	writeFile(t, FilePath(dir), "contexts: [\n")
	if ok, err := ReadFile(dir, &s); ok || err == nil {
		t.Fatalf("broken file: ok=%v err=%v", ok, err)
	}
	writeFile(t, FilePath(dir), "contexts: 5\n")
	if _, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)}); err == nil {
		t.Fatal("a wrongly typed file must fail the load")
	}
}

func TestReadFileLegacyFlatFormat(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	writeFile(t, FilePath(dir),
		"server: https://legacy.example.com\nflavor: selfhosted\nauth:\n  scheme: pat\n")
	got, exists, err := readTool(dir)
	if err != nil || !exists {
		t.Fatalf("ReadFile: exists=%v err=%v", exists, err)
	}
	if len(got.Contexts) != 1 || got.Contexts[0].Name != DefaultContextName {
		t.Fatalf("legacy file should yield one default context: %+v", got.Contexts)
	}
	if got.CurrentContext != DefaultContextName {
		t.Errorf("CurrentContext = %q, want default", got.CurrentContext)
	}
	if got.Contexts[0].BaseURL != "https://legacy.example.com" {
		t.Errorf("BaseURL = %q", got.Contexts[0].BaseURL)
	}
}

func TestLoadLegacyFlatUnchanged(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	writeFile(t, FilePath(dir), "server: https://file.example.com\nflavor: selfhosted\n")
	got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.BaseURL != "https://file.example.com" || got.Config.Flavor != flavorSelf {
		t.Errorf("legacy load mismatch: %+v", got.Config)
	}
	if got.Sources[KeyServer] != "file" {
		t.Errorf("server source = %q, want file", got.Sources[KeyServer])
	}
	if got.ActiveContext != DefaultContextName {
		t.Errorf("ActiveContext = %q, want default", got.ActiveContext)
	}
}

func writeMultiContextFile(t *testing.T, dir string) {
	t.Helper()
	writeFile(t, FilePath(dir), `current_context: alpha
contexts:
  - name: alpha
    server: https://alpha.example.com
    flavor: selfhosted
    auth: {scheme: pat}
  - name: beta
    server: https://beta.example.com
    flavor: cloud
    auth: {scheme: basic, username: bob}
defaults:
  format: json
`)
}

func TestLoadMultiContextSelection(t *testing.T) {
	t.Run("current_context", func(t *testing.T) {
		useTool(t, nil)
		dir := t.TempDir()
		writeMultiContextFile(t, dir)
		got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
		if err != nil {
			t.Fatal(err)
		}
		if got.ActiveContext != "alpha" || got.Config.BaseURL != "https://alpha.example.com" {
			t.Errorf("active=%q url=%q", got.ActiveContext, got.Config.BaseURL)
		}
		if len(got.ContextNames) != 2 {
			t.Errorf("ContextNames = %v", got.ContextNames)
		}
	})

	t.Run("flag overrides current_context", func(t *testing.T) {
		useTool(t, nil)
		dir := t.TempDir()
		writeMultiContextFile(t, dir)
		got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir), Context: "BETA"})
		if err != nil {
			t.Fatal(err)
		}
		if got.ActiveContext != "beta" || got.Config.BaseURL != "https://beta.example.com" || got.ContextSource != ContextSourceFlag {
			t.Errorf("active=%q url=%q source=%q", got.ActiveContext, got.Config.BaseURL, got.ContextSource)
		}
	})

	t.Run("env overrides current_context", func(t *testing.T) {
		useTool(t, nil)
		dir := t.TempDir()
		writeMultiContextFile(t, dir)
		t.Setenv("TOOL_CONTEXT", "beta")
		got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
		if err != nil {
			t.Fatal(err)
		}
		if got.ActiveContext != "beta" || got.ContextSource != ContextSourceEnv {
			t.Errorf("active = %q source = %q, want beta from env", got.ActiveContext, got.ContextSource)
		}
	})

	t.Run("TOOL_SERVER still overrides selected context", func(t *testing.T) {
		useTool(t, nil)
		dir := t.TempDir()
		writeMultiContextFile(t, dir)
		t.Setenv("TOOL_SERVER", "https://override.example.com")
		got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir)})
		if err != nil {
			t.Fatal(err)
		}
		if got.Config.BaseURL != "https://override.example.com" {
			t.Errorf("BaseURL = %q, want env override", got.Config.BaseURL)
		}
	})

	t.Run("unknown context errors", func(t *testing.T) {
		useTool(t, nil)
		dir := t.TempDir()
		writeMultiContextFile(t, dir)
		_, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir), Context: "ghost"})
		if ce := cerrors.AsCLIError(err); ce == nil || ce.Code != "UNKNOWN_CONTEXT" {
			t.Errorf("want UNKNOWN_CONTEXT, got %v", err)
		}
	})

	t.Run("setup targets a new context without selecting", func(t *testing.T) {
		useTool(t, nil)
		dir := t.TempDir()
		writeMultiContextFile(t, dir)
		t.Setenv("TOOL_CONTEXT", "ghost")
		got, err := loadTool(toolLoad{ConfigDir: dir, DotenvPath: absent(dir), Context: "fresh", Setup: true})
		if err != nil {
			t.Fatal(err)
		}
		if got.ActiveContext != "fresh" || got.ContextSource != ContextSourceFlag || got.Config.BaseURL != "" {
			t.Errorf("active=%q source=%q url=%q", got.ActiveContext, got.ContextSource, got.Config.BaseURL)
		}
	})
}

func TestSelectContext(t *testing.T) {
	useTool(t, nil)
	f := toolFile{
		CurrentContext: "alpha",
		Contexts:       []toolContext{{Name: "alpha"}, {Name: "beta"}},
	}
	cases := []struct {
		name, flag, env, want, wantSource string
		wantErr                           bool
	}{
		{name: "flag wins", flag: "beta", want: "beta", wantSource: ContextSourceFlag},
		{name: "env over current", env: "beta", want: "beta", wantSource: ContextSourceEnv},
		{name: "current_context", want: "alpha", wantSource: ContextSourceCurrent},
		{name: "unknown flag errors", flag: "ghost", wantErr: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got, source, err := selectContext(f, tc.flag, tc.env)
			if tc.wantErr {
				if err == nil {
					t.Fatal("expected error")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("got %q, want %q", got, tc.want)
			}
			if source != tc.wantSource {
				t.Errorf("source = %q, want %q", source, tc.wantSource)
			}
		})
	}

	t.Run("sole context", func(t *testing.T) {
		got, source, err := selectContext(toolFile{Contexts: []toolContext{{Name: "only"}}}, "", "")
		if err != nil || got != "only" || source != ContextSourceSingle {
			t.Errorf("got %q source %q err %v", got, source, err)
		}
	})
	t.Run("default-named context", func(t *testing.T) {
		got, source, err := selectContext(toolFile{Contexts: []toolContext{{Name: "a"}, {Name: "default"}}}, "", "")
		if err != nil || got != "default" || source != ContextSourceDefault {
			t.Errorf("got %q source %q err %v", got, source, err)
		}
	})
	t.Run("ambiguous yields empty no error", func(t *testing.T) {
		got, source, err := selectContext(toolFile{Contexts: []toolContext{{Name: "a"}, {Name: "b"}}}, "", "")
		if err != nil || got != "" || source != ContextSourceNone {
			t.Errorf("got %q source %q err %v, want empty/none/no-error", got, source, err)
		}
	})
	t.Run("no file yields empty", func(t *testing.T) {
		got, source, err := selectContext(toolFile{}, "", "")
		if err != nil || got != "" || source != ContextSourceNone {
			t.Errorf("got %q source %q err %v", got, source, err)
		}
	})

	// UNKNOWN_CONTEXT must surface a structured CLIError (not a generic load
	// failure) so the caller sees the code and hint.
	t.Run("unknown returns UNKNOWN_CONTEXT cli error", func(t *testing.T) {
		_, _, err := selectContext(f, "ghost", "")
		ce, ok := err.(*cerrors.CLIError)
		if !ok || ce.Code != "UNKNOWN_CONTEXT" {
			t.Fatalf("got %T %v, want *CLIError UNKNOWN_CONTEXT", err, err)
		}
		if ce.Hint == "" || len(ce.NextSteps) != 1 || ce.NextSteps[0] != "tool config get-contexts" {
			t.Errorf("hint %q next steps %v", ce.Hint, ce.NextSteps)
		}
	})

	// Case-insensitive lookup returns the canonical name, so a persisted
	// reference agrees with the contexts list.
	t.Run("case-insensitive lookup returns canonical name", func(t *testing.T) {
		ff := toolFile{Contexts: []toolContext{{Name: "Cloud"}, {Name: "default"}}}
		got, _, err := selectContext(ff, "cloud", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != "Cloud" {
			t.Errorf("got %q, want canonical %q", got, "Cloud")
		}
	})

	t.Run("hint lists available contexts", func(t *testing.T) {
		ff := toolFile{Contexts: []toolContext{{Name: "alpha"}, {Name: "beta"}}}
		_, _, err := selectContext(ff, "gamma", "")
		ce := err.(*cerrors.CLIError)
		if !strings.Contains(ce.Hint, "alpha") || !strings.Contains(ce.Hint, "beta") {
			t.Errorf("hint = %q, want both context names listed", ce.Hint)
		}
	})
}

// TestResolveConfigDir covers the default directory and the optional legacy
// one. HOME points at a temp directory so the real user's config is never
// touched.
func TestResolveConfigDir(t *testing.T) {
	withFakeHome := func(t *testing.T) string {
		t.Helper()
		home := t.TempDir()
		t.Setenv("HOME", home)
		t.Setenv("USERPROFILE", home)
		return home
	}

	t.Run("resolves to ~/.vendor/tool", func(t *testing.T) {
		useTool(t, nil)
		home := withFakeHome(t)
		dir, err := ResolveDir()
		if err != nil {
			t.Fatal(err)
		}
		if want := filepath.Join(home, ".vendor", "tool"); dir != want {
			t.Errorf("dir = %q, want %q", dir, want)
		}
		if legacy, err := LegacyDir(); legacy != "" || err != nil {
			t.Errorf("LegacyDir = %q, %v; want none registered", legacy, err)
		}
	})

	t.Run("legacy directory", func(t *testing.T) {
		useTool(t, func(s *Schema) { s.Paths.Legacy = ".tool" })
		home := withFakeHome(t)
		current, legacy := filepath.Join(home, ".vendor", "tool"), filepath.Join(home, ".tool")
		check := func(want string) {
			t.Helper()
			if dir, err := ResolveDir(); err != nil || dir != want {
				t.Errorf("ResolveDir = %q, %v; want %q", dir, err, want)
			}
		}
		check(current) // neither has a file
		if err := os.MkdirAll(legacy, 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, FilePath(legacy), "contexts: []\n")
		check(legacy) // only the legacy directory has one
		if err := os.MkdirAll(current, 0o700); err != nil {
			t.Fatal(err)
		}
		writeFile(t, FilePath(current), "contexts: []\n")
		check(current) // both: the current directory wins
		if dir, err := DefaultDir(); err != nil || dir != current {
			t.Errorf("DefaultDir = %q, %v", dir, err)
		}
		if dir, err := LegacyDir(); err != nil || dir != legacy {
			t.Errorf("LegacyDir = %q, %v", dir, err)
		}
	})
}
