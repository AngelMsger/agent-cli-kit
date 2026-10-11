package auth

import (
	"errors"
	"net/http"
	"reflect"
	"testing"

	"github.com/zalando/go-keyring"

	"github.com/angelmsger/agent-cli-kit/credstore"
	cerrors "github.com/angelmsger/agent-cli-kit/errors"
	"github.com/angelmsger/agent-cli-kit/transport"
)

// testProduct registers a product shaped like the family's CLIs: a bearer
// default, Basic with a custom message, None, and a Custom scheme with hooks.
func testProduct(t *testing.T, edit func(*Product)) {
	t.Helper()
	p := &Product{
		Schemes: []Scheme{
			{Name: "pat", Kind: Bearer, Missing: "no Personal Access Token configured"},
			{Name: "basic", Kind: Basic, Missing: "basic auth requires both a username and a password"},
			{Name: "token", Kind: Basic, MissingCode: "AUTH_NO_TOKEN"},
			{Name: "none", Kind: None},
			{Name: "raw", Kind: Custom, Header: func(c Credential) string { return "Raw " + c.Secret }},
		},
		DefaultScheme:    "pat",
		Name:             "Widget",
		Doctor:           "tool doctor",
		MissingStep:      "Run `tool config init`.",
		InaccessibleStep: "Do not run `config init` yet.",
	}
	if edit != nil {
		edit(p)
	}
	Register(p)
	t.Cleanup(func() { Register(nil) })
}

func TestValidate(t *testing.T) {
	testProduct(t, nil)
	cases := []struct {
		cred Credential
		code string
		msg  string
	}{
		{Credential{Scheme: "pat", Secret: "s"}, "", ""},
		{Credential{Scheme: "pat"}, "AUTH_NO_TOKEN", "no Personal Access Token configured"},
		{Credential{Scheme: "basic", Username: "u", Secret: "s"}, "", ""},
		{Credential{Scheme: "basic", Secret: "s"}, "AUTH_NO_BASIC", "basic auth requires both a username and a password"},
		{Credential{Scheme: "token", Secret: "s"}, "AUTH_NO_TOKEN", "token auth requires both a username and a secret"},
		{Credential{Scheme: "none"}, "", ""},
		{Credential{Scheme: "raw"}, "AUTH_NO_TOKEN", "raw auth requires a secret"},
		{Credential{Scheme: "kerberos"}, "AUTH_BAD_SCHEME", `unknown auth scheme "kerberos"`},
	}
	for _, tc := range cases {
		err := tc.cred.Validate()
		if tc.code == "" {
			if err != nil {
				t.Errorf("%+v: unexpected error %v", tc.cred, err)
			}
			continue
		}
		ce := cerrors.AsCLIError(err)
		if ce.Code != tc.code || ce.Message != tc.msg || ce.Category != cerrors.CategoryConfig {
			t.Errorf("%+v: got %s %q (%s), want %s %q", tc.cred, ce.Code, ce.Message, ce.Category, tc.code, tc.msg)
		}
	}
}

func TestValidateListsSchemes(t *testing.T) {
	for _, tc := range []struct {
		names []string
		want  string
	}{
		{[]string{"token", "basic"}, `unknown auth scheme "x" (want token or basic)`},
		{[]string{"none", "basic", "bearer"}, `unknown auth scheme "x" (want none, basic or bearer)`},
	} {
		Register(&Product{ListSchemes: true, Schemes: schemesNamed(tc.names...)})
		if got := cerrors.AsCLIError(Credential{Scheme: "x"}.Validate()).Message; got != tc.want {
			t.Errorf("got %q, want %q", got, tc.want)
		}
	}
	Register(nil)
}

func schemesNamed(names ...string) []Scheme {
	out := make([]Scheme, len(names))
	for i, n := range names {
		out[i] = Scheme{Name: n, Kind: Basic}
	}
	return out
}

func TestValidateHook(t *testing.T) {
	sentinel := cerrors.New(cerrors.CategoryConfig, "AUTH_BAD_SESSION", "bad")
	testProduct(t, func(p *Product) {
		p.Schemes = append(p.Schemes, Scheme{Name: "session", Kind: Custom, Validate: func(c Credential) error {
			if c.Secret == "ok" {
				return nil
			}
			return sentinel
		}})
	})
	if err := (Credential{Scheme: "session", Secret: "ok"}).Validate(); err != nil {
		t.Fatalf("hook accepted value rejected: %v", err)
	}
	if err := (Credential{Scheme: "session", Secret: "no"}).Validate(); err != sentinel {
		t.Fatalf("hook error not returned: %v", err)
	}
}

func TestHeaderAndDecorator(t *testing.T) {
	testProduct(t, func(p *Product) {
		p.Schemes = append(p.Schemes, Scheme{Name: "cookie", Kind: Custom, Decorate: func(c Credential) transport.Decorator {
			return func(req *http.Request) { req.Header.Set("Cookie", c.Secret) }
		}})
	})
	cases := []struct {
		cred   Credential
		header string
	}{
		{Credential{Scheme: "pat", Secret: "tok"}, "Bearer tok"},
		{Credential{Scheme: "basic", Username: "u", Secret: "p"}, "Basic dTpw"},
		{Credential{Scheme: "none", Secret: "ignored"}, ""},
		{Credential{Scheme: "raw", Secret: "abc"}, "Raw abc"},
		{Credential{Scheme: "cookie", Secret: "a=b"}, ""},
		{Credential{Scheme: "unknown", Secret: "s"}, ""},
	}
	for _, tc := range cases {
		if got := tc.cred.Header(); got != tc.header {
			t.Errorf("%s Header() = %q, want %q", tc.cred.Scheme, got, tc.header)
		}
		req, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
		tc.cred.Decorator()(req)
		if got := req.Header.Get("Authorization"); got != tc.header {
			t.Errorf("%s decorator set Authorization %q, want %q", tc.cred.Scheme, got, tc.header)
		}
	}
	req, _ := http.NewRequest(http.MethodGet, "https://example.test", nil)
	Credential{Scheme: "cookie", Secret: "a=b"}.Decorator()(req)
	if req.Header.Get("Cookie") != "a=b" {
		t.Fatal("Decorate hook was not used")
	}
}

func TestRedacted(t *testing.T) {
	for in, want := range map[string]string{"": "", "abc": "****", "abcd": "****", "secret-value": "********alue"} {
		if got := (Credential{Secret: in}).Redacted().Secret; got != want {
			t.Errorf("Redacted(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAccountKey(t *testing.T) {
	testProduct(t, nil)
	cases := map[string]string{
		"https://jira.example.com":      "jira.example.com:pat",
		"https://example.com/jira/":     "example.com:pat",
		"https://example.com:8443/prom": "example.com:8443:pat",
		"not a url":                     "not a url:pat",
	}
	for in, want := range cases {
		if got := AccountKey(in, "pat"); got != want {
			t.Errorf("AccountKey(%q) = %q, want %q", in, got, want)
		}
	}

	testProduct(t, func(p *Product) { p.AccountKeyIncludesPath = true })
	if got := AccountKey("https://example.com/tenant-a/", "basic"); got != "example.com/tenant-a:basic" {
		t.Errorf("path-keyed AccountKey = %q", got)
	}
	if got := AccountKey("https://example.com/", "basic"); got != "example.com:basic" {
		t.Errorf("path-keyed AccountKey without a path = %q", got)
	}
}

// memBackend is an in-memory keychain. getErr, when set, is returned by Get.
type memBackend struct {
	m      map[string]string
	getErr error
}

func (b *memBackend) Get(service, account string) (string, error) {
	if b.getErr != nil {
		return "", b.getErr
	}
	if v, ok := b.m[service+"/"+account]; ok {
		return v, nil
	}
	return "", keyring.ErrNotFound
}

func (b *memBackend) Set(service, account, secret string) error {
	b.m[service+"/"+account] = secret
	return nil
}

func (b *memBackend) Delete(service, account string) error {
	delete(b.m, service+"/"+account)
	return nil
}

func newStore(t *testing.T) (*credstore.Store, *memBackend) {
	b := &memBackend{m: map[string]string{}}
	return credstore.NewStoreWithBackend(t.TempDir(), "tool", b), b
}

func TestResolve(t *testing.T) {
	testProduct(t, nil)
	store, backend := newStore(t)
	const base = "https://jira.example.com"
	if _, err := store.Save(AccountKey(base, "pat"), "stored"); err != nil {
		t.Fatal(err)
	}

	cred, err := Resolve(Request{BaseURL: base}, store)
	if err != nil || cred != (Credential{Scheme: "pat", Secret: "stored"}) {
		t.Fatalf("default scheme from store: %+v, %v", cred, err)
	}

	cred, err = Resolve(Request{BaseURL: base, Secrets: map[string]string{"pat": "flag"}}, store)
	if err != nil || cred.Secret != "flag" {
		t.Fatalf("supplied secret should win: %+v, %v", cred, err)
	}

	looked := ""
	cred, err = Resolve(Request{BaseURL: "https://other", LookupURL: func() (string, error) { looked = base; return base, nil }}, store)
	if err != nil || cred.Secret != "stored" || looked == "" {
		t.Fatalf("LookupURL should key the store: %+v, %v", cred, err)
	}

	if _, err := Resolve(Request{BaseURL: base, Secrets: map[string]string{"pat": "flag"},
		LookupURL: func() (string, error) { t.Fatal("LookupURL ran without a store lookup"); return "", nil }}, store); err != nil {
		t.Fatal(err)
	}

	mismatch := ServiceMismatchError()
	if _, err := Resolve(Request{BaseURL: base, LookupURL: func() (string, error) { return "", mismatch }}, store); err != mismatch {
		t.Fatalf("LookupURL error not returned: %v", err)
	}

	_, err = Resolve(Request{Scheme: "basic", BaseURL: base, Username: "u"}, store)
	if ce := cerrors.AsCLIError(err); ce.Code != "CREDENTIAL_NOT_VISIBLE_OR_MISSING" {
		t.Fatalf("missing secret: %v", err)
	}

	_, err = Resolve(Request{Scheme: "basic", BaseURL: base}, nil)
	if ce := cerrors.AsCLIError(err); ce.Code != "AUTH_NO_BASIC" {
		t.Fatalf("no store should fall through to Validate: %v", err)
	}

	backend.getErr = errors.New("sandbox denied keychain")
	_, err = Resolve(Request{Scheme: "basic", BaseURL: base, Username: "u"}, store)
	var access *credstore.StoreAccessError
	if ce := cerrors.AsCLIError(err); ce.Code != "CREDENTIAL_STORE_INACCESSIBLE" || !errors.As(err, &access) {
		t.Fatalf("inaccessible store: %v", err)
	}
}

func TestResolveNoneSkipsStore(t *testing.T) {
	testProduct(t, func(p *Product) { p.DefaultScheme = "none" })
	store, backend := newStore(t)
	backend.getErr = errors.New("must not be read")
	cred, err := Resolve(Request{BaseURL: "https://prom.example.com", Username: "u"}, store)
	if err != nil || cred != (Credential{Scheme: "none", Username: "u"}) {
		t.Fatalf("none scheme: %+v, %v", cred, err)
	}
}

func TestMissingSteps(t *testing.T) {
	testProduct(t, nil)
	store, _ := newStore(t)
	want := []string{hostRetryStep, "tool doctor", "Run `tool config init`."}
	_, err := Resolve(Request{BaseURL: "https://x"}, store)
	if got := cerrors.AsCLIError(err).NextSteps; !reflect.DeepEqual(got, want) {
		t.Fatalf("default steps = %q", got)
	}

	_, err = Resolve(Request{BaseURL: "https://x", MissingSteps: []string{"Reuse.", "Sign in."}}, store)
	want = []string{hostRetryStep, "tool doctor", "Reuse.", "Sign in."}
	if got := cerrors.AsCLIError(err).NextSteps; !reflect.DeepEqual(got, want) {
		t.Fatalf("per-call steps = %q", got)
	}
}

func TestSaveAndForget(t *testing.T) {
	testProduct(t, nil)
	store, _ := newStore(t)
	const base = "https://jira.example.com"

	if _, err := Save(base, Credential{Scheme: "basic", Secret: "p"}, store); cerrors.AsCLIError(err).Code != "AUTH_NO_BASIC" {
		t.Fatalf("Save must validate first: %v", err)
	}
	backend, err := Save(base, Credential{Scheme: "basic", Username: "u", Secret: "p"}, store)
	if err != nil || backend != credstore.BackendKeychain {
		t.Fatalf("Save: %q, %v", backend, err)
	}
	if got, _ := store.Load(AccountKey(base, "basic")); got != "p" {
		t.Fatalf("stored %q", got)
	}
	if backend, err := Save(base, Credential{Scheme: "none"}, store); err != nil || backend != BackendNone {
		t.Fatalf("Save none: %q, %v", backend, err)
	}

	if err := Forget(base, "basic", store); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Load(AccountKey(base, "basic")); !errors.Is(err, credstore.ErrSecretNotFound) {
		t.Fatalf("Forget left the secret: %v", err)
	}
}

func TestServiceMismatchGuidance(t *testing.T) {
	testProduct(t, nil)
	cerrors.SetGuidance(func(cerrors.Category) (string, []string) { return "default hint", []string{"default step"} })
	t.Cleanup(func() { cerrors.SetGuidance(nil) })

	if e := ServiceMismatchError(); e.Hint != "default hint" || !reflect.DeepEqual(e.NextSteps, []string{"default step"}) {
		t.Fatalf("without product wording the category guidance applies: %+v", e)
	}
	testProduct(t, func(p *Product) {
		p.MismatchHint = "Select the right context."
		p.MismatchSteps = []string{"tool config get-contexts"}
	})
	if e := ServiceMismatchError(); e.Hint != "Select the right context." || !reflect.DeepEqual(e.NextSteps, []string{"tool config get-contexts"}) {
		t.Fatalf("product wording not applied: %+v", e)
	}
}

func TestRegisterCopies(t *testing.T) {
	p := &Product{Schemes: []Scheme{{Name: "pat", Kind: Bearer}}}
	Register(p)
	t.Cleanup(func() { Register(nil) })
	p.Schemes[0].Name = "changed"
	if _, ok := lookup("pat"); !ok {
		t.Fatal("Register must copy the scheme table")
	}
}
