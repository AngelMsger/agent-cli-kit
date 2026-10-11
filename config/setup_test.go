package config

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

func setupFixture() toolContext {
	return toolContext{Name: "team", BaseURL: "https://service.example.test/deploy", Auth: toolAuth{Scheme: "pat", Username: "PersonalUser", CredentialURL: "https://help.example.test/tokens"}, Flavor: flavorSelf}
}

func TestSetupTargetsNamedContextAndExcludesPersonalEnvironment(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	target := setupFixture()
	file := toolFile{CurrentContext: "other", Contexts: []toolContext{{Name: "other", BaseURL: "https://other.example.test", Auth: toolAuth{Scheme: "pat"}}, target}}
	if err := writeTool(dir, file); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TOOL_CONTEXT", "not-defined")
	t.Setenv("TOOL_FORMAT", "ndjson")
	t.Setenv("TOOL_USERNAME", "InjectedUser")
	t.Setenv("TOOL_API_TOKEN", "must-not-be-copied")
	dotenv := filepath.Join(dir, "presets.env")
	if err := os.WriteFile(dotenv, []byte("TOOL_SERVER=https://dotenv.example.test/deploy\nTOOL_CREDENTIAL_URL=https://help.example.test/new\nTOOL_API_TOKEN=dotenv-secret\nTOOL_USERNAME=dotenv-user\n"), 0600); err != nil {
		t.Fatal(err)
	}
	opt := toolLoad{ConfigDir: dir, DotenvPath: dotenv, Context: "TEAM", Setup: true}
	got, err := loadTool(opt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.Defaults.Format != "ndjson" {
		t.Fatal("setup ignored the output format environment variable")
	}
	if got.ActiveContext != "team" || got.Config.BaseURL != "https://dotenv.example.test/deploy" || got.Config.Auth.Username != "PersonalUser" || got.Config.Auth.Scheme != "pat" {
		t.Fatalf("wrong target or personal override: %+v", got)
	}
	if !reflect.DeepEqual(got.Secrets, toolSecrets{}) {
		t.Fatalf("setup retained secrets: %+v", got.Secrets)
	}
	if got.Sources[KeyServer] != "dotenv" || got.Sources[KeyCredentialURL] != "dotenv" {
		t.Fatal(got.Sources)
	}
	t.Setenv("TOOL_SERVER", "https://env.example.test")
	t.Setenv("TOOL_AUTH_SCHEME", "basic")
	got, err = loadTool(opt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.BaseURL != "https://env.example.test" || got.Config.Auth.Scheme != "basic" || got.Sources[KeyServer] != "env" {
		t.Fatal(got)
	}
	opt.Flags = toolFlags{BaseURL: "https://flag.example.test", CredentialURL: "https://help.example.test/flag", AuthScheme: "pat"}
	got, err = loadTool(opt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.BaseURL != "https://flag.example.test" || got.Sources[KeyServer] != "flag" || got.Sources[KeyCredentialURL] != "flag" {
		t.Fatal(got)
	}
	opt.Context = "new"
	opt.Flags = toolFlags{}
	t.Setenv("TOOL_SERVER", "")
	opt.DotenvPath = filepath.Join(dir, "absent")
	got, err = loadTool(opt)
	if err != nil {
		t.Fatal(err)
	}
	if got.Config.BaseURL != "" || got.Config.Auth.Username != "" {
		t.Fatalf("new target inherited active context: %+v", got)
	}
}

func TestPlanServiceContextConflictIdempotencyAndPreservation(t *testing.T) {
	useTool(t, nil)
	dir := t.TempDir()
	target := setupFixture()
	file := toolFile{CurrentContext: "other", Contexts: []toolContext{{Name: "other", BaseURL: "https://other.example.test"}, target}, Defaults: toolDefaults{ReadOnly: true, Project: "KEEP"}}
	if err := writeTool(dir, file); err != nil {
		t.Fatal(err)
	}
	opt := toolLoad{ConfigDir: dir, Context: "TEAM", Setup: true, DotenvPath: filepath.Join(dir, "absent")}
	resolved, err := loadTool(opt)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := planTool(file, "TEAM", resolved, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Changed {
		t.Fatalf("repeat is not idempotent: %+v", plan.Changes)
	}
	opt.Flags.BaseURL = "https://new.example.test/deploy"
	resolved, err = loadTool(opt)
	if err != nil {
		t.Fatal(err)
	}
	_, err = planTool(file, "team", resolved, false, false)
	ce := cerrors.AsCLIError(err)
	if ce == nil || ce.Code != "CONFIG_CONTEXT_CONFLICT" || ce.Details == nil {
		t.Fatalf("missing structured differences: %v", err)
	}
	plan, err = planTool(file, "team", resolved, true, false)
	if err != nil {
		t.Fatal(err)
	}
	nc, _ := plan.File.Context("team")
	if nc.Auth != target.Auth || plan.File.CurrentContext != "other" || !reflect.DeepEqual(plan.File.Defaults, file.Defaults) || plan.File.Contexts[0] != file.Contexts[0] {
		t.Fatalf("clobbered unrelated values: %+v", plan.File)
	}
	if err := writeTool(dir, plan.File); err != nil {
		t.Fatal(err)
	}
	roundtrip, _, err := readTool(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(roundtrip, plan.File) {
		t.Fatalf("roundtrip lost fields: got %+v want %+v", roundtrip, plan.File)
	}
	plan, err = planTool(file, "team", resolved, true, true)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CurrentContext != "team" {
		t.Fatal(plan)
	}
	initial := &toolResolved{Config: resolved.Config, Resolved: &Resolved{Sources: resolved.Sources}}
	plan, err = planTool(toolFile{}, "team", initial, false, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.CurrentContext != "team" {
		t.Fatal("first context must activate")
	}
}

func TestCredentialGuideIsDisplayOnlyAndKeepsDeploymentPath(t *testing.T) {
	useTool(t, nil)
	nc := setupFixture()
	cfg := toolConfig{BaseURL: nc.BaseURL, Auth: nc.Auth, Flavor: flavorSelf}
	cfg.Auth.CredentialURL = ""
	g, err := toolGuide(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	if g.CredentialURL != nc.BaseURL {
		t.Fatalf("wrong default URL: %+v", g)
	}
	if len(g.Instructions) == 0 || g.DocumentationURL == "" || len(g.NextSteps) == 0 {
		t.Fatal(g)
	}
	cfg.Auth.CredentialURL = "https://login.example.test/custom?view=credentials"
	g, err = toolGuide(cfg, map[string]string{KeyCredentialURL: "env"})
	if err != nil {
		t.Fatal(err)
	}
	if g.CredentialURL != cfg.Auth.CredentialURL || g.Server != nc.BaseURL || g.Source != "env" {
		t.Fatal(g)
	}
	raw, _ := json.Marshal(g)
	if strings.Contains(string(raw), "PersonalUser") {
		t.Fatal("guide should not expose personal identity")
	}
	for _, bad := range []string{"javascript:alert(1)", "https://user:secret@example.test", "/relative", "https://example.test/\npath"} {
		cfg.Auth.CredentialURL = bad
		if _, err := toolGuide(cfg, nil); err == nil {
			t.Fatalf("accepted unsafe link %q", bad)
		}
	}
}

func TestNormalizeServiceIdentityAndRejectInvalidInput(t *testing.T) {
	useTool(t, nil)
	normal, err := NormalizeServiceURL("https://SERVICE.example.test:443/deploy/")
	if err != nil || normal != "https://service.example.test/deploy" {
		t.Fatalf("%q %v", normal, err)
	}
	for _, bad := range []string{"", "file:///tmp/token", "https://user:secret@example.test", "https://example.test/?token=secret", "https://example.test/#fragment", "service.example.test"} {
		if _, err := NormalizeServiceURL(bad); err == nil {
			t.Fatalf("accepted invalid service URL %q", bad)
		}
	}
}

func TestSetupRecoveryQuotesContextNames(t *testing.T) {
	useTool(t, nil)
	nc := setupFixture()
	resolved := &toolResolved{Config: toolConfig{BaseURL: nc.BaseURL, Auth: nc.Auth, Flavor: flavorSelf}, Resolved: &Resolved{}}
	plan, err := planTool(toolFile{}, "ops team's", resolved, false, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, step := range plan.NextSteps {
		if !strings.Contains(step, `--use-context 'ops team'"'"'s'`) {
			t.Fatalf("context was not safely quoted: %s", step)
		}
	}
}
