package config

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"

	cerrors "github.com/angelmsger/agent-cli-kit/errors"
	"github.com/angelmsger/agent-cli-kit/output"
)

// wireGuidance makes the category defaults visible in the pinned JSON, so a
// test fails when an error stops overriding them or starts to.
func wireGuidance(t *testing.T) {
	t.Helper()
	cerrors.SetGuidance(func(cat cerrors.Category) (string, []string) {
		return "Default " + string(cat) + " hint.", []string{"tool " + string(cat) + " --help"}
	})
	t.Cleanup(func() { cerrors.SetGuidance(nil) })
}

// payload encodes an error's envelope as output.EmitError does, without the
// indentation.
func payload(t *testing.T, err error) string {
	t.Helper()
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if e := enc.Encode(cerrors.AsCLIError(err).Payload()); e != nil {
		t.Fatal(e)
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

// TestErrorWireFormat pins the configuration errors an agent branches on.
// Change them only on purpose, with a CHANGELOG entry and every CLI's docs.
func TestErrorWireFormat(t *testing.T) {
	useTool(t, nil)
	wireGuidance(t)
	two := toolFile{CurrentContext: "ghost", Contexts: []toolContext{{Name: "alpha"}, {Name: "beta"}}}
	planOf := func(file toolFile, name, server string, sources map[string]string) error {
		r := &toolResolved{Config: toolConfig{BaseURL: server, Auth: toolAuth{Scheme: "pat"}}, Resolved: &Resolved{Sources: sources}}
		_, err := planTool(file, name, r, false, false)
		return err
	}
	_, _, fromFlag := selectContext(two, "gamma", "")
	_, _, fromEnv := selectContext(toolFile{}, "", "ghost")
	_, _, fromCurrent := selectContext(two, "", "")
	_, lastErr := DeleteContext(&toolFile{Contexts: []toolContext{{Name: "only"}}}, "only")
	existing := toolFile{Contexts: []toolContext{{Name: "team", BaseURL: "https://old.example.com", Auth: toolAuth{Scheme: "pat"}}}}

	cases := []struct {
		name string
		err  error
		want string
	}{
		{"unknown context from the flag", fromFlag,
			`{"error":{"category":"config","code":"UNKNOWN_CONTEXT",` +
				`"message":"context \"gamma\" (from --use-context) is not defined in the config file",` +
				`"hint":"Available contexts: alpha, beta.","next_steps":["tool config get-contexts"],"retryable":false}}`},
		{"unknown context from the variable", fromEnv,
			`{"error":{"category":"config","code":"UNKNOWN_CONTEXT",` +
				`"message":"context \"ghost\" (from TOOL_CONTEXT) is not defined in the config file",` +
				"\"hint\":\"Run `tool config get-contexts` to list defined contexts.\"," +
				`"next_steps":["tool config get-contexts"],"retryable":false}}`},
		{"unknown current context", fromCurrent,
			`{"error":{"category":"config","code":"UNKNOWN_CONTEXT",` +
				`"message":"context \"ghost\" (from current_context) is not defined in the config file",` +
				`"hint":"Available contexts: alpha, beta.","next_steps":["tool config get-contexts"],"retryable":false}}`},
		{"unknown context argument", UnknownContextError("ghost", []string{"alpha", "beta"}),
			`{"error":{"category":"config","code":"UNKNOWN_CONTEXT","message":"context \"ghost\" is not defined",` +
				`"hint":"Available contexts: alpha, beta.","next_steps":["tool config get-contexts"],"retryable":false}}`},
		{"no service URL", planOf(toolFile{}, "team", "", nil),
			`{"error":{"category":"config","code":"NO_BASE_URL","message":"no service URL configured",` +
				`"hint":"Default config hint.","next_steps":["tool config set-context <name> --base-url <url>"],"retryable":false}}`},
		{"bad service URL", planOf(toolFile{}, "team", "https://user:pw@example.com", nil),
			`{"error":{"category":"config","code":"BAD_BASE_URL",` +
				`"message":"service URL must be an HTTP(S) URL without credentials, query or fragment",` +
				`"hint":"Default config hint.","next_steps":["tool config --help"],"retryable":false}}`},
		{"bad credential URL", ValidateService(Values{KeyServer: "https://s.example.com", KeyCredentialURL: "/x", KeyAuthScheme: "pat"}),
			`{"error":{"category":"config","code":"BAD_CREDENTIAL_URL",` +
				`"message":"credential URL must be an absolute HTTP(S) page URL without embedded credentials",` +
				`"hint":"Default config hint.","next_steps":["tool config --help"],"retryable":false}}`},
		{"bad scheme", ValidateService(Values{KeyServer: "https://s.example.com", KeyAuthScheme: "bearer"}),
			`{"error":{"category":"config","code":"AUTH_BAD_SCHEME","message":"unsupported authentication scheme",` +
				`"hint":"Use one of: pat, basic.","next_steps":["tool config --help"],"retryable":false}}`},
		{"empty context name", planOf(toolFile{}, " ", "https://s.example.com", nil),
			`{"error":{"category":"usage","code":"CTX_NAME_EMPTY","message":"provide a non-empty context name",` +
				`"hint":"Default usage hint.","next_steps":["tool usage --help"],"retryable":false}}`},
		{"conflict", planOf(existing, "team", "https://new.example.com", map[string]string{KeyServer: "flag"}),
			`{"error":{"details":{"server":{"before":"https://old.example.com","after":"https://new.example.com"}},` +
				`"category":"conflict","code":"CONFIG_CONTEXT_CONFLICT","message":"team presets conflict with the existing context",` +
				`"hint":"Inspect the field differences; use --overwrite to update the supplied service fields, or choose another context name.",` +
				`"next_steps":["tool config set-context --help"],"retryable":false}}`},
		{"last context", lastErr,
			`{"error":{"category":"usage","code":"LAST_CONTEXT","message":"cannot delete the only context",` +
				`"hint":"Default usage hint.","next_steps":["tool usage --help"],"retryable":false}}`},
	}
	for _, tc := range cases {
		if got := payload(t, tc.err); got != tc.want {
			t.Errorf("%s payload JSON changed:\n got %s\nwant %s", tc.name, got, tc.want)
		}
	}
}

// TestPlanAndGuideWireFormat pins the set-context result and the auth guide.
func TestPlanAndGuideWireFormat(t *testing.T) {
	useTool(t, nil)
	file := toolFile{CurrentContext: "other", Contexts: []toolContext{{Name: "other"}, {Name: "team", BaseURL: "https://old.example.com"}}}
	r := &toolResolved{Config: toolConfig{BaseURL: "HTTPS://New.Example.com:443/deploy/", Auth: toolAuth{Scheme: "pat"}},
		Resolved: &Resolved{Sources: map[string]string{KeyServer: "flag"}}}
	plan, err := planTool(file, "TEAM", r, true, true)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(plan)
	if err != nil {
		t.Fatal(err)
	}
	const wantPlan = `{"context":"team","changed":true,"current_context":"team",` +
		`"changes":{"auth.scheme":{"before":"","after":"pat"},"current_context":{"before":"other","after":"team"},` +
		`"server":{"before":"https://old.example.com","after":"https://new.example.com/deploy"}},` +
		`"next_steps":["tool --use-context 'team' auth guide","tool --use-context 'team' auth login"]}`
	if string(raw) != wantPlan {
		t.Errorf("plan JSON changed:\n got %s\nwant %s", raw, wantPlan)
	}

	g := AuthGuide{Server: "https://s.example.com", Scheme: "pat", CredentialURL: "https://s.example.com/tokens", Source: "builtin",
		Instructions: []string{"Create a token."}, DocumentationURL: "https://docs.example.com", NextSteps: []string{"tool auth login"}}
	raw, err = json.Marshal(g)
	if err != nil {
		t.Fatal(err)
	}
	const wantGuide = `{"server":"https://s.example.com","scheme":"pat","credential_url":"https://s.example.com/tokens",` +
		`"source":"builtin","instructions":["Create a token."],"documentation_url":"https://docs.example.com","next_steps":["tool auth login"]}`
	if string(raw) != wantGuide {
		t.Errorf("guide JSON changed:\n got %s\nwant %s", raw, wantGuide)
	}
	g.Flavor = flavorCloud
	if raw, _ = json.Marshal(g); !bytes.Contains(raw, []byte(`"server":"https://s.example.com","flavor":"cloud","scheme":"pat"`)) {
		t.Errorf("flavor is written after the server when set: %s", raw)
	}
}

// TestContextHintWireFormat pins the --help reminder block and the _notice
// payload for an implicitly chosen context.
func TestContextHintWireFormat(t *testing.T) {
	useTool(t, nil)
	var buf bytes.Buffer
	output.EmitNotice(&buf, ContextNotice("alpha", ContextSourceCurrent, []string{"alpha", "beta"}))
	const wantNotice = `{"_notice":{"context":{"active":"alpha","available":["alpha","beta"],` +
		`"message":"Multiple contexts are configured and this one was selected implicitly (selected via the saved current_context). ` +
		`Confirm \"alpha\" is the intended target before trusting results.",` +
		`"override":"--use-context <name> or TOOL_CONTEXT=<name>","selected_via":"current_context",` +
		`"silence":"set TOOL_CLI_NO_CONTEXT_HINT=1 to suppress"}}}` + "\n"
	if buf.String() != wantNotice {
		t.Errorf("notice changed:\n got %s\nwant %s", buf.String(), wantNotice)
	}
	buf.Reset()
	output.EmitNotice(&buf, ContextNotice("", ContextSourceNone, []string{"a", "b"}))
	if !bytes.Contains(buf.Bytes(), []byte(`(no context selected). Confirm \"the resolved context\" is`)) {
		t.Errorf("notice without an active context: %s", buf.String())
	}

	const wantBlock = "Active context: alpha  (2 configured: alpha, beta; selected via TOOL_CONTEXT)\n" +
		"Multiple contexts are configured — verify you are targeting the intended one.\n" +
		"Override per call with --use-context <name> or TOOL_CONTEXT=<name>, or\n" +
		"change the default with `tool config use-context <name>`.\n"
	if got := ReminderBlock("alpha", ContextSourceEnv, []string{"alpha", "beta"}); got != wantBlock {
		t.Errorf("reminder block changed:\n got %q\nwant %q", got, wantBlock)
	}
	if got := ReminderBlock("", ContextSourceNone, []string{"a", "b"}); got[:47] != "No active context selected (2 configured: a, b)" {
		t.Errorf("reminder block without an active context: %q", got)
	}
	for source, want := range map[string]string{
		ContextSourceFlag:    "selected via --use-context",
		ContextSourceEnv:     "selected via TOOL_CONTEXT",
		ContextSourceCurrent: "selected via the saved current_context",
		ContextSourceSingle:  "the only configured context",
		ContextSourceDefault: `selected as the "default" context`,
		ContextSourceNone:    "no context selected",
	} {
		if got := SourceLabel(source); got != want {
			t.Errorf("SourceLabel(%q) = %q, want %q", source, got, want)
		}
	}
}
