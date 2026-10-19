package reuse

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/angelmsger/agent-cli-kit/auth"
	"github.com/angelmsger/agent-cli-kit/config"
	"github.com/angelmsger/agent-cli-kit/credstore"
	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

func TestMain(m *testing.M) {
	auth.Register(&auth.Product{
		Schemes: []auth.Scheme{
			{Name: "none", Kind: auth.None},
			{Name: "basic", Kind: auth.Basic},
			{Name: "token", Kind: auth.Custom, MissingCode: "AUTH_NO_TOKEN"},
			{Name: "session", Kind: auth.Custom, Session: true, Validate: func(c auth.Credential) error {
				if !strings.HasPrefix(c.Secret, "session:") {
					return cerrors.New(cerrors.CategoryConfig, "AUTH_BAD_SESSION", "bad session")
				}
				return nil
			}},
		},
		DefaultScheme: "basic",
		Name:          "Example",
	})
	config.Register(&config.Schema{Words: config.Words{
		App: "tool", ContextFlag: "--use-context", ListContexts: "config get-contexts",
		SetContext: "config set-context", AuthGuide: "auth guide", AuthLogin: "auth login",
	}})
	os.Exit(m.Run())
}

const (
	ops = "https://o2.ops.example.com"
	dev = "https://o2.dev.example.com"
)

// env is a fake credential store and service. Secrets are keyed like the real
// store; a secret authenticates when accounts names it.
type env struct {
	keychain        map[string]string
	file            map[string]string
	keychainSkipped bool
	keychainFailed  bool
	accounts        map[string]string // secret (and "user|secret" for basic) → account ID
	verifyErr       map[string]error
	slowVerify      map[string]time.Duration
	slowLookup      map[string]time.Duration
	persistErr      error

	lookups, verifies int
	persisted         *Update
}

func newEnv() *env {
	return &env{keychain: map[string]string{}, file: map[string]string{}, accounts: map[string]string{},
		verifyErr: map[string]error{}, slowVerify: map[string]time.Duration{}, slowLookup: map[string]time.Duration{}}
}

func key(base, scheme string) string { return auth.AccountKey(base, scheme) }

func (e *env) lookup(k string) credstore.Lookup {
	e.lookups++
	if d := e.slowLookup[k]; d > 0 {
		time.Sleep(d)
	}
	var l credstore.Lookup
	switch {
	case e.keychainSkipped:
		l.Keychain = credstore.Skipped
	case e.keychainFailed:
		l.Keychain, l.KeychainErr = credstore.Failed, errors.New("locked")
	default:
		if v, ok := e.keychain[k]; ok {
			l.Secret, l.Keychain = v, credstore.Hit
			return l
		}
		l.Keychain = credstore.Miss
	}
	if v, ok := e.file[k]; ok {
		l.Secret, l.File = v, credstore.Hit
	} else {
		l.File = credstore.Miss
	}
	return l
}

func (e *env) resolve(s Spec) (auth.Credential, credstore.Lookup, error) {
	l := e.lookup(key(s.KeyBase, s.Scheme))
	switch err := l.Err(); {
	case errors.Is(err, credstore.ErrSecretNotFound):
		return auth.Credential{}, l, auth.MissingError(nil)
	case err != nil:
		return auth.Credential{}, l, auth.InaccessibleError(err)
	}
	cred := auth.Credential{Scheme: s.Scheme, Username: s.Username, Secret: l.Secret}
	if err := cred.Validate(); err != nil {
		return auth.Credential{}, l, err
	}
	return cred, l, nil
}

func (e *env) verify(ctx context.Context, _ Spec, c auth.Credential) (Account, error) {
	e.verifies++
	id := c.Secret
	if auth.UsesUsername(c.Scheme) {
		id = c.Username + "|" + c.Secret
	}
	if d := e.slowVerify[id]; d > 0 {
		select {
		case <-time.After(d):
		case <-ctx.Done():
			return Account{}, cerrors.New(cerrors.CategoryNetwork, "NETWORK", "deadline")
		}
	}
	if err := e.verifyErr[id]; err != nil {
		return Account{}, err
	}
	if account, ok := e.accounts[id]; ok {
		return Account{ID: account}, nil
	}
	return Account{}, cerrors.New(cerrors.CategoryAuth, "AUTH_FAILED", "rejected").WithHTTPStatus(401)
}

func ctxOf(name, base, scheme, user string) Context {
	return Context{Name: name, Service: base + "|default", KeyBase: base, Scheme: scheme, Username: user}
}

func (e *env) request(target string, contexts ...Context) Request {
	var t Context
	for _, c := range contexts {
		if c.Name == target {
			t = c
		}
	}
	return Request{
		Target: target, Contexts: contexts, Runtime: t, Check: "auth status",
		Key: key, Resolve: e.resolve, Lookup: e.lookup, Verify: e.verify,
		Persist: func(u Update) error {
			if e.persistErr != nil {
				return e.persistErr
			}
			e.persisted = &u
			return nil
		},
	}
}

func codeOf(err error) string {
	if err == nil {
		return ""
	}
	return cerrors.AsCLIError(err).Code
}

// liveOpenObserve is the owner's machine: three services, one personal basic
// context, browser-session team contexts, and basic secrets no context points at.
func liveOpenObserve() []Context {
	return []Context{
		ctxOf("default", ops, "basic", "me@example.com"),
		ctxOf("test", dev, "session", "me@example.com"),
		ctxOf("prod[browser]", ops, "session", ""),
		ctxOf("dev", dev, "session", ""),
		ctxOf("prod-jdy", ops, "session", ""),
	}
}

func liveEnv() *env {
	e := newEnv()
	e.keychain[key(ops, "basic")] = "ops-pw"
	e.keychain[key(dev, "basic")] = "dev-pw"
	e.accounts["me@example.com|ops-pw"] = ""
	e.accounts["me@example.com|dev-pw"] = ""
	return e
}

func TestSessionTeamContextAdoptsPersonalBasic(t *testing.T) { // S1
	e := liveEnv()
	res, err := Run(e.request("prod-jdy", liveOpenObserve()...))
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateReused || res.SourceContext != "default" || res.Scheme != "basic" || res.Username != "me@example.com" {
		t.Fatalf("got %+v", res)
	}
	if e.persisted == nil || e.persisted.Scheme != "basic" || !e.persisted.SetUsername {
		t.Fatalf("persisted %+v", e.persisted)
	}
}

func TestWorkingSessionIsKept(t *testing.T) { // S4, C7
	e := liveEnv()
	e.keychain[key(ops, "session")] = "session:ok"
	e.accounts["session:ok"] = ""
	res, err := Run(e.request("prod-jdy", liveOpenObserve()...))
	if err != nil || res.State != StateUnchanged || !res.Verified || e.persisted != nil {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestRejectedSessionFallsBackToBasic(t *testing.T) {
	e := liveEnv()
	e.keychain[key(ops, "session")] = "session:expired"
	res, err := Run(e.request("prod-jdy", liveOpenObserve()...))
	if err != nil || res.State != StateReused || res.Scheme != "basic" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestLabelledSessionAdoptsOrphanBasic(t *testing.T) { // S2
	e := liveEnv()
	res, err := Run(e.request("test", liveOpenObserve()...))
	if err != nil {
		t.Fatal(err)
	}
	if res.State != StateReused || res.SourceContext != "" || res.Scheme != "basic" || res.Username != "me@example.com" {
		t.Fatalf("got %+v", res)
	}
	if e.persisted.SetUsername || e.persisted.BaseURL != "" {
		t.Fatalf("persisted %+v", e.persisted)
	}
}

func TestSharedSessionKeyNoLongerFails(t *testing.T) { // S3, C5
	e := liveEnv()
	contexts := liveOpenObserve()
	res, err := Run(e.request("dev", contexts...))
	if err != nil {
		t.Fatalf("dev: %v", err)
	}
	if res.State != StateReused || res.SourceContext != "" || res.Username != "me@example.com" {
		t.Fatalf("dev: %+v", res)
	}
	// After dev adopted basic, test finds the same credential through dev.
	for i, c := range contexts {
		if c.Name == "dev" {
			contexts[i].Scheme, contexts[i].Username = "basic", "me@example.com"
		}
	}
	e.persisted = nil
	res, err = Run(e.request("test", contexts...))
	if err != nil || res.State != StateReused || res.SourceContext != "dev" {
		t.Fatalf("test: %+v, %v", res, err)
	}
}

func TestSharedSessionKeyInSandboxKeepsHostRetry(t *testing.T) { // S11, C5 sandbox
	e := liveEnv()
	e.keychainSkipped = true
	req := e.request("dev", liveOpenObserve()...)
	req.SchemeSupplied = true
	_, err := Run(req)
	if codeOf(err) != "CREDENTIAL_NOT_VISIBLE_OR_MISSING" {
		t.Fatalf("pinned sandbox: %v", err)
	}
	_, err = Run(e.request("dev", liveOpenObserve()...))
	if codeOf(err) != "CREDENTIAL_NOT_VISIBLE_OR_MISSING" {
		t.Fatalf("unpinned sandbox: %v", err)
	}
}

func TestWitnessLookupTimeoutKeepsTheError(t *testing.T) { // S23
	e := liveEnv()
	e.slowLookup[key(dev, "basic")] = 300 * time.Millisecond
	req := e.request("dev", liveOpenObserve()...)
	req.Window = 50 * time.Millisecond
	_, err := Run(req)
	if codeOf(err) != "CREDENTIAL_NOT_VISIBLE_OR_MISSING" {
		t.Fatalf("got %v", err)
	}
}

func TestIdentityShortCircuits(t *testing.T) { // S5
	e := liveEnv()
	res, err := Run(e.request("default", liveOpenObserve()...))
	if err != nil || res.State != StateUnchanged || e.lookups != 0 || e.verifies != 0 {
		t.Fatalf("got %+v, %v, lookups %d", res, err, e.lookups)
	}
}

func TestLabelledWithoutCandidates(t *testing.T) { // S6
	e := newEnv()
	contexts := []Context{ctxOf("solo", dev, "session", "me@example.com")}
	res, err := Run(e.request("solo", contexts...))
	if err != nil || res.State != StateUnchanged || e.verifies != 0 {
		t.Fatalf("unpinned: %+v, %v", res, err)
	}
	e = newEnv()
	req := e.request("solo", contexts...)
	req.SchemeSupplied = true
	res, err = Run(req)
	if err != nil || res.State != StateUnchanged || e.lookups != 0 || e.verifies != 0 {
		t.Fatalf("pinned: %+v, %v", res, err)
	}
}

func TestPinnedCallerMakesNoCrossSchemeChecks(t *testing.T) { // S11, D2
	e := liveEnv()
	req := e.request("test", liveOpenObserve()...)
	req.SchemeSupplied = true
	res, err := Run(req)
	if err != nil || res.State != StateUnchanged || e.lookups != 0 {
		t.Fatalf("got %+v, %v, lookups %d", res, err, e.lookups)
	}
	req = e.request("prod-jdy", liveOpenObserve()...)
	req.SchemeSupplied = true
	e.keychain[key(ops, "session")] = "session:expired"
	res, err = Run(req)
	if err != nil || res.State != StateUnavailable || e.persisted != nil {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestMissingFirstStepSource(t *testing.T) { // S7, C6
	contexts := []Context{
		ctxOf("team", ops, "token", ""),
		ctxOf("a-personal", ops+"/", "token", "x"), // other spelling, other key? same host → same key
	}
	_ = contexts
	alt := "https://o2.ops.example.com:443"
	contexts = []Context{ctxOf("team", ops, "token", ""), {Name: "a-gone", Service: ops + "|default", KeyBase: alt, Scheme: "token"}, {Name: "b-live", Service: ops + "|default", KeyBase: "https://O2.ops.example.com:443", Scheme: "token"}}
	e := newEnv()
	e.keychain[key(ops, "token")] = "team-old"
	e.keychain[key("https://O2.ops.example.com:443", "token")] = "live"
	e.accounts["live"] = ""
	res, err := Run(e.request("team", contexts...))
	if err != nil || res.State != StateReused || res.SourceContext != "b-live" {
		t.Fatalf("witnessed: %+v, %v", res, err)
	}
	e = newEnv()
	e.keychainSkipped = true
	e.file[key(ops, "token")] = "team-old"
	e.file[key("https://O2.ops.example.com:443", "token")] = "live"
	e.accounts["live"] = ""
	_, err = Run(e.request("team", contexts...))
	if codeOf(err) != "CREDENTIAL_NOT_VISIBLE_OR_MISSING" {
		t.Fatalf("unwitnessed: %v", err)
	}
}

func TestContinuationIsBoundedByTheWindow(t *testing.T) { // S24
	alt, alt2 := "https://o2.ops.example.com:443", "https://O2.ops.example.com:443"
	contexts := []Context{ctxOf("team", ops, "token", ""), {Name: "a-gone", Service: ops + "|default", KeyBase: alt, Scheme: "token"}, {Name: "b-slow", Service: ops + "|default", KeyBase: alt2, Scheme: "token"}}
	e := newEnv()
	e.keychain[key(ops, "token")] = "team-old"
	e.keychain[key(alt2, "token")] = "slow"
	e.accounts["slow"] = ""
	e.slowVerify["slow"] = 2 * time.Second
	req := e.request("team", contexts...)
	req.Window = 50 * time.Millisecond
	started := time.Now()
	_, err := Run(req)
	if codeOf(err) != "CREDENTIAL_NOT_VISIBLE_OR_MISSING" || time.Since(started) > time.Second {
		t.Fatalf("got %v after %v", err, time.Since(started))
	}
}

func TestUsernameLessBasicContextsAreNotLookedUp(t *testing.T) {
	contexts := []Context{ctxOf("team", ops, "basic", ""), {Name: "a-empty", Service: ops + "|default", KeyBase: "https://o2.ops.example.com:443", Scheme: "basic"}, ctxOf("b-me", ops, "basic", "me")}
	e := newEnv()
	e.keychain[key(ops, "basic")] = "pw"
	e.accounts["me|pw"] = ""
	res, err := Run(e.request("team", contexts...))
	if err != nil || res.State != StateReused || res.SourceContext != "b-me" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestTwoLongLivedLoginsAreAmbiguous(t *testing.T) { // S8
	e := newEnv()
	contexts := []Context{ctxOf("team", ops, "session", "")}
	e.keychain[key(ops, "basic")] = "pw"
	e.keychain[key(ops, "token")] = "tok"
	e.accounts["tok"] = ""
	contexts = append(contexts, ctxOf("me", ops, "basic", "me"))
	e.accounts["me|pw"] = ""
	_, err := Run(e.request("team", contexts...))
	if codeOf(err) != "AUTH_REUSE_AMBIGUOUS" {
		t.Fatalf("got %v", err)
	}
	details := cerrors.AsCLIError(err).Details.(map[string]any)
	if len(details["candidates"].([]Candidate)) != 2 {
		t.Fatalf("details %+v", details)
	}
}

func TestOneAccountIsOneLogin(t *testing.T) { // S9
	e := newEnv()
	e.keychain[key(ops, "basic")] = "pw"
	e.keychain[key(ops, "token")] = "tok"
	e.accounts["tok"] = "acct-1"
	e.accounts["me|pw"] = "acct-1"
	contexts := []Context{ctxOf("team", ops, "session", ""), ctxOf("me", ops, "basic", "me")}
	res, err := Run(e.request("team", contexts...))
	if err != nil || res.State != StateReused || res.Account != "acct-1" || res.SourceContext != "me" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestLabelRestrictsUsernameCandidates(t *testing.T) { // S10
	e := newEnv()
	e.keychain[key(ops, "basic")] = "pw"
	e.accounts["other|pw"] = ""
	contexts := []Context{ctxOf("team", ops, "session", "me"), ctxOf("other", ops, "basic", "other")}
	res, err := Run(e.request("team", contexts...))
	// The label's own pairing is rejected and the session is reliably absent,
	// so the labelled context is reported as having no login.
	if err != nil || res.State != StateUnavailable || e.persisted != nil {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestUnreliableStoreDoesNotCrossSchemes(t *testing.T) { // S12, C11
	e := liveEnv()
	e.keychainSkipped = true
	e.file = e.keychain
	e.keychain = map[string]string{}
	res, err := Run(e.request("prod-jdy", liveOpenObserve()...))
	if err != nil || res.State != StateUnavailable || e.persisted != nil {
		t.Fatalf("got %+v, %v", res, err)
	}
	if len(res.NextSteps) == 0 || !strings.HasPrefix(res.NextSteps[0], "Retry") {
		t.Fatalf("next steps %v", res.NextSteps)
	}
}

func TestSlowServiceStopsAtTheWindow(t *testing.T) { // S13
	e := liveEnv()
	e.slowVerify["me@example.com|ops-pw"] = 2 * time.Second
	req := e.request("prod-jdy", liveOpenObserve()...)
	req.Window = 50 * time.Millisecond
	started := time.Now()
	res, err := Run(req)
	if err != nil || res.State != StateUnavailable || time.Since(started) > time.Second {
		t.Fatalf("got %+v, %v after %v", res, err, time.Since(started))
	}
}

func TestDryRunAndWriteSafeguards(t *testing.T) { // S16, S17
	e := liveEnv()
	req := e.request("prod-jdy", liveOpenObserve()...)
	req.DryRun = true
	res, err := Run(req)
	if err != nil || res.State != StateAvailable || e.persisted != nil || !strings.Contains(res.NextSteps[0], "auth reuse") {
		t.Fatalf("dry run: %+v, %v", res, err)
	}
	e.persistErr = ErrConfigChanged
	if _, err = Run(e.request("prod-jdy", liveOpenObserve()...)); codeOf(err) != "AUTH_REUSE_CONFIG_CHANGED" {
		t.Fatalf("config changed: %v", err)
	}
	e.persistErr = errors.New("read-only")
	if _, err = Run(e.request("prod-jdy", liveOpenObserve()...)); codeOf(err) != "AUTH_REUSE_WRITE_FAILED" {
		t.Fatalf("write failed: %v", err)
	}
}

func TestSessionIsTheLastStep(t *testing.T) { // S19
	e := newEnv()
	e.keychain[key(ops, "basic")] = "stale"
	e.keychain[key(ops, "session")] = "session:ok"
	e.accounts["session:ok"] = ""
	contexts := []Context{ctxOf("team", ops, "token", ""), ctxOf("me", ops, "basic", "me"), ctxOf("browser", ops, "session", "")}
	res, err := Run(e.request("team", contexts...))
	if err != nil || res.State != StateReused || res.Scheme != "session" {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestStoredSecretsNeedASingleServiceOnTheirKey(t *testing.T) { // S20
	e := liveEnv()
	contexts := append(liveOpenObserve(), ctxOf("http", "http://o2.dev.example.com", "token", ""))
	res, err := Run(e.request("test", contexts...))
	if err != nil || res.State != StateUnchanged || e.persisted != nil {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestExplicitSelections(t *testing.T) { // S4, S21
	e := liveEnv()
	e.keychain[key(dev, "session")] = "session:ok"
	e.accounts["session:ok"] = ""
	req := e.request("test", liveOpenObserve()...)
	req.FromScheme = "basic"
	res, err := Run(req)
	if err != nil || res.State != StateReused || res.Scheme != "basic" {
		t.Fatalf("from-scheme: %+v, %v", res, err)
	}

	e = newEnv()
	e.keychain[key(ops, "basic")] = "pw"
	e.accounts["a|pw"], e.accounts["b|pw"] = "", ""
	contexts := []Context{ctxOf("team", ops, "token", ""), ctxOf("x", ops, "session", "a"), ctxOf("y", ops, "session", "b")}
	if _, err = Run(e.request("team", contexts...)); codeOf(err) != "AUTH_REUSE_AMBIGUOUS" {
		t.Fatalf("pairings: %v", err)
	}
	req = e.request("team", contexts...)
	req.FromScheme, req.From = "basic", "x"
	res, err = Run(req)
	if err != nil || res.State != StateReused || res.Username != "a" {
		t.Fatalf("paired: %+v, %v", res, err)
	}
}

func TestLabelledAdoptionNeedsReliableEvidence(t *testing.T) { // S22
	alt := "https://o2.dev.example.com:443"
	contexts := []Context{ctxOf("test", dev, "session", "me"), {Name: "other", Service: dev + "|default", KeyBase: alt, Scheme: "session"}}
	e := newEnv()
	e.keychainSkipped = true
	e.file[key(dev, "session")] = "session:stale"
	e.file[key(alt, "session")] = "session:ok"
	e.accounts["session:ok"] = ""
	res, err := Run(e.request("test", contexts...))
	if err != nil || res.State != StateUnchanged || e.persisted != nil {
		t.Fatalf("got %+v, %v", res, err)
	}
}

func TestSameSchemeFromContextKeepsTheEarlierBehavior(t *testing.T) { // S25
	e := liveEnv()
	req := e.request("test", liveOpenObserve()...)
	req.From = "dev"
	res, err := Run(req)
	if err != nil || res.State != StateUnchanged || e.lookups != 0 {
		t.Fatalf("got %+v, %v", res, err)
	}
	req = e.request("prod-jdy", liveOpenObserve()...)
	req.SchemeSupplied, req.FromScheme = true, "basic"
	if _, err = Run(req); codeOf(err) != "AUTH_REUSE_SOURCE_MISMATCH" {
		t.Fatalf("pinned from-scheme: %v", err)
	}
}

func TestTargetMismatchAndMissing(t *testing.T) {
	e := liveEnv()
	req := e.request("prod-jdy", liveOpenObserve()...)
	req.Runtime.Scheme = "basic"
	if _, err := Run(req); codeOf(err) != "AUTH_REUSE_TARGET_MISMATCH" {
		t.Fatalf("mismatch: %v", err)
	}
	if _, err := Run(e.request("nope", liveOpenObserve()...)); codeOf(err) != "AUTH_REUSE_TARGET_MISSING" {
		t.Fatalf("missing: %v", err)
	}
}

func TestMayReuse(t *testing.T) {
	contexts := liveOpenObserve()
	find := func(name string) Context { c, _ := find(contexts, name); return c }
	if !MayReuse(find("prod-jdy"), contexts, "", false) || !MayReuse(find("test"), contexts, "", false) {
		t.Fatal("a session context could reuse a long-lived login")
	}
	if MayReuse(find("default"), contexts, "", false) {
		t.Fatal("a context with an identity needs no reuse")
	}
	if MayReuse(find("prod-jdy"), contexts, "", true) {
		t.Fatal("with a supplied scheme only same-scheme sources with a login of their own count")
	}
	if MayReuse(find("prod-jdy"), contexts, "someone", false) {
		t.Fatal("a runtime username is an identity")
	}
}

func TestNothingStoredIsAPlainNoChange(t *testing.T) {
	e := newEnv()
	res, err := Run(e.request("team", ctxOf("team", ops, "session", "")))
	if err != nil || res.State != StateUnavailable || e.verifies != 0 {
		t.Fatalf("got %+v, %v", res, err)
	}
	for _, step := range res.NextSteps {
		if strings.HasPrefix(step, "Retry") {
			t.Fatalf("a host retry is offered although the keychain answered: %v", res.NextSteps)
		}
	}
}

func TestALoginIsTriedOnce(t *testing.T) {
	e := newEnv()
	e.keychain[key(ops, "basic")] = "stale"
	contexts := []Context{ctxOf("team", ops, "basic", ""), ctxOf("me", ops, "basic", "me")}
	res, err := Run(e.request("team", contexts...))
	if err != nil || res.State != StateUnavailable || e.verifies != 1 {
		t.Fatalf("got %+v, %v after %d verifications", res, err, e.verifies)
	}
}

func TestAnUnauthenticatedContextIsNoSource(t *testing.T) {
	e := newEnv()
	req := e.request("team", ctxOf("team", ops, "basic", ""), ctxOf("open", ops, "none", ""))
	req.From = "open"
	if _, err := Run(req); codeOf(err) != "AUTH_REUSE_SOURCE_MISMATCH" || e.verifies != 0 {
		t.Fatalf("got %v after %d verifications", err, e.verifies)
	}
}

func TestAnExplicitSourceMustKeepTheDestinationValid(t *testing.T) {
	e := liveEnv()
	req := e.request("prod-jdy", liveOpenObserve()...)
	req.Valid = func(scheme string) bool { return scheme != "basic" }
	req.From = "default"
	if _, err := Run(req); codeOf(err) != "AUTH_REUSE_SOURCE_MISMATCH" || e.lookups != 0 {
		t.Fatalf("from-context: %v after %d lookups", err, e.lookups)
	}
	req.From, req.FromScheme = "", "basic"
	if _, err := Run(req); codeOf(err) != "AUTH_REUSE_SOURCE_MISMATCH" || e.lookups != 0 {
		t.Fatalf("from-scheme: %v after %d lookups", err, e.lookups)
	}
}
