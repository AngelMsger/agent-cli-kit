// Package reuse lets a context adopt a login the user already verified for the
// same service, so nobody signs in again while a usable credential is stored.
//
// It implements `auth reuse` for every CLI of the family: the CLI describes
// its stored contexts and supplies resolution, verification and persistence;
// this package decides. The rules are the family standard's "Reuse an existing
// login": sources are considered in three steps (contexts under the
// destination's scheme, long-lived credentials under any scheme, browser
// sessions), a working login is never replaced automatically, and every check
// the command did not make before runs after the earlier result, within one
// deadline, and may only replace that result with a better one.
package reuse

import (
	"context"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/angelmsger/agent-cli-kit/auth"
	"github.com/angelmsger/agent-cli-kit/config"
	"github.com/angelmsger/agent-cli-kit/credstore"
	cerrors "github.com/angelmsger/agent-cli-kit/errors"
)

// DefaultWindow bounds the checks the command did not make before.
const DefaultWindow = 10 * time.Second

// Result states.
const (
	StateAvailable   = "available"
	StateReused      = "reused"
	StateUnchanged   = "unchanged"
	StateUnavailable = "unavailable"
)

// ErrConfigChanged is what Request.Persist returns when the configuration file
// no longer matches the one the decision was made from.
var ErrConfigChanged = errors.New("configuration changed during verification")

// Context is one stored context as reuse sees it.
type Context struct {
	Name string
	// Service identifies the service: the complete normalized URL plus the
	// provider scope (flavor, organization, tenant). Contexts with equal
	// values are the same service; "" never matches anything.
	Service string
	// KeyBase is the URL spelling the context's credential key derives from.
	KeyBase string
	// Scheme is the effective auth scheme, defaults applied.
	Scheme   string
	Username string
}

// Spec names one credential to resolve or verify.
type Spec struct {
	Scheme   string
	Username string
	KeyBase  string
	// Context is the context whose stored configuration this is; "" for a
	// secret stored for the destination's service that no context points at.
	Context string
}

// Account is the server-side account a credential authenticated as, when the
// service's authentication check reports one. ID is the canonical identifier;
// Name is for display.
type Account struct {
	ID   string
	Name string
}

// Update is the change to the destination context.
type Update struct {
	Scheme string
	// Username is written only when SetUsername is true; otherwise the
	// destination keeps the username or label it has.
	Username    string
	SetUsername bool
	// BaseURL, when not empty, replaces the destination's URL spelling so it
	// resolves the source's credential key.
	BaseURL string
}

// Request is one `auth reuse` invocation.
type Request struct {
	Target   string
	Contexts []Context
	// Runtime is the service and scheme in effect for this invocation, after
	// flags, environment and .env; its Name is ignored.
	Runtime Context
	// SchemeSupplied is true when the invocation supplied a scheme by flag,
	// environment, .env or an environment credential.
	SchemeSupplied bool
	// SchemeFlag is the scheme the invocation passed by flag, if any. Every
	// command this package suggests repeats it, so following a step keeps the
	// constraint; a scheme from the environment or .env carries over by itself.
	SchemeFlag string
	From       string
	FromScheme string
	DryRun     bool

	// Start is when the command started; the deadline counts from it.
	Start time.Time
	// Window is the deadline for checks the command did not make before;
	// DefaultWindow when zero. Timeout, the request timeout, narrows it.
	Window  time.Duration
	Timeout time.Duration
	Now     func() time.Time

	// Check is the command that reaches the service, such as "auth status".
	Check string
	// LoginStep returns the sign-in subcommand for a scheme, such as
	// "auth login"; the registered Words.AuthLogin when nil.
	LoginStep func(scheme string) string

	// Key derives a credential key; Valid reports whether the destination
	// stays a valid configuration under a scheme; SameUsername compares
	// usernames (exact when nil).
	Key          func(baseURL, scheme string) string
	Valid        func(scheme string) bool
	SameUsername func(a, b string) bool

	// Resolve resolves a context's credential, or the destination's, exactly
	// as other commands do, and reports what the store answered.
	Resolve func(Spec) (auth.Credential, credstore.Lookup, error)
	// Lookup reads the stored secret for a key directly.
	Lookup func(key string) credstore.Lookup
	// Verify authenticates cred against the destination's service.
	Verify func(ctx context.Context, spec Spec, cred auth.Credential) (Account, error)
	// Persist rereads the configuration, returns ErrConfigChanged when it
	// differs from the one the decision was made from, and writes u.
	Persist func(u Update) error
}

// Result is the JSON `auth reuse` prints.
type Result struct {
	Context       string   `json:"context"`
	SourceContext string   `json:"source_context,omitempty"`
	State         string   `json:"state"`
	Changed       bool     `json:"changed"`
	Verified      bool     `json:"verified"`
	DryRun        bool     `json:"dry_run"`
	Scheme        string   `json:"scheme,omitempty"`
	Username      string   `json:"username,omitempty"`
	Account       string   `json:"account,omitempty"`
	AccountName   string   `json:"account_name,omitempty"`
	Reason        string   `json:"reason,omitempty"`
	NextSteps     []string `json:"next_steps,omitempty"`
}

// Candidate lists one source in AUTH_REUSE_AMBIGUOUS details.
type Candidate struct {
	Context  string `json:"context,omitempty"`
	Scheme   string `json:"scheme"`
	Username string `json:"username,omitempty"`
	Account  string `json:"account,omitempty"`
}

// Run decides and, unless DryRun, applies one reuse.
func Run(req Request) (Result, error) {
	r := newRun(req)
	res, err := r.run()
	if err == nil {
		res.NextSteps = r.nextSteps(res)
	}
	return res, err
}

type usability int

const (
	unknown usability = iota
	usable
	unusable
)

// evidence is what a check showed. Whether it is reliable is decided when it
// is used, since any later keychain answer in the run counts as the witness.
type evidence struct {
	state   usability
	settled bool // reliable regardless of the store: configuration, or judged already
	looked  bool
	lookup  credstore.Lookup
	err     error
}

type candidate struct {
	spec    Spec
	cred    auth.Credential
	lookup  credstore.Lookup
	account Account
	ev      evidence
}

type run struct {
	req      Request
	words    config.Words
	target   Context
	usesUser bool
	labelled bool
	witness  bool
	deadline time.Time
	result   Result

	abstained   bool
	storeHidden bool // the keychain failed or was skipped for some lookup
	preloaded   bool

	// The answer the command gave before, returned whenever new work cannot
	// reach a better conclusion.
	origResult Result
	origErr    error
	improved   *Result

	targetChecked bool
	targetEv      evidence
	targetLookup  *credstore.Lookup
	earlier       []*candidate // every source of the earlier steps
}

func newRun(req Request) *run {
	if req.Now == nil {
		req.Now = time.Now
	}
	if req.Start.IsZero() {
		req.Start = req.Now()
	}
	if req.Window <= 0 {
		req.Window = DefaultWindow
	}
	if req.SameUsername == nil {
		req.SameUsername = func(a, b string) bool { return a == b }
	}
	if req.Valid == nil {
		req.Valid = func(string) bool { return true }
	}
	window := req.Window
	if req.Timeout > 0 && req.Timeout < window {
		window = req.Timeout
	}
	return &run{req: req, words: config.RegisteredWords(), deadline: req.Start.Add(window)}
}

func (r *run) run() (Result, error) {
	req := r.req
	r.result = Result{Context: req.Target, State: StateUnavailable, DryRun: req.DryRun}
	target, ok := find(req.Contexts, req.Target)
	if !ok || target.Service == "" {
		return r.result, cerrors.New(cerrors.CategoryConfig, "AUTH_REUSE_TARGET_MISSING", "prepare the destination context before reusing a login").
			WithNextSteps(r.step(r.words.SetContext + " <name> --base-url <url>"))
	}
	r.target, r.result.Context = target, target.Name
	if req.Runtime.Service != target.Service || req.Runtime.Scheme != target.Scheme {
		return r.result, cerrors.New(cerrors.CategoryConflict, "AUTH_REUSE_TARGET_MISMATCH", "service overrides differ from the stored destination; no identity was changed").
			WithNextSteps(r.selected() + " config show")
	}
	var from Context
	if req.From != "" {
		if from, ok = find(req.Contexts, req.From); !ok {
			return r.result, cerrors.New(cerrors.CategoryNotFound, "AUTH_REUSE_SOURCE_NOT_FOUND", "the selected source context does not exist").
				WithNextSteps(config.ListContextsStep())
		}
		if from.Service != target.Service || (req.SchemeSupplied && req.FromScheme == "" && from.Scheme != target.Scheme) {
			return r.result, r.sourceMismatch("the source context belongs to a different service or scope")
		}
		if req.FromScheme == "" && !auth.NeedsCredential(from.Scheme) {
			return r.result, r.sourceMismatch("the source context does not use authentication")
		}
	}
	if req.FromScheme != "" {
		if err := (auth.Credential{Scheme: req.FromScheme}).Validate(); err != nil && cerrors.AsCLIError(err).Code == "AUTH_BAD_SCHEME" {
			return r.result, err
		}
		if !auth.NeedsCredential(req.FromScheme) || (req.SchemeSupplied && req.FromScheme != target.Scheme) {
			return r.result, r.sourceMismatch("the selected scheme cannot supply a login for this destination")
		}
	}
	if !auth.NeedsCredential(target.Scheme) {
		r.result.State, r.result.Reason = StateUnchanged, "the service does not use authentication"
		return r.result, nil
	}
	r.usesUser = auth.UsesUsername(target.Scheme)
	if r.usesUser && target.Username != "" {
		r.result.State, r.result.Reason = StateUnchanged, "destination identity is already configured"
		return r.result, nil
	}
	r.labelled = !r.usesUser && target.Username != ""
	if req.FromScheme != "" || (req.From != "" && from.Scheme != target.Scheme) {
		return r.explicit(from)
	}
	return r.automatic()
}

// automatic is reuse without a selected source of another scheme: the first
// step as before, then the checks the command did not make before.
func (r *run) automatic() (Result, error) {
	first := r.firstStep()
	if r.labelled {
		// The earlier answer for a labelled destination was an immediate
		// unchanged; an explicitly selected same-scheme source keeps it.
		base := r.unchanged("destination identity is already configured")
		if r.req.SchemeSupplied || r.req.From != "" {
			return base, nil
		}
		r.origResult = base
		return r.settle(r.evaluate(first, later))
	}

	// The baseline: the first step as before, with the pre-access exclusions
	// and the stand-in lookup. It stops where the command stopped before.
	out := r.evaluate(first, baseline)
	base, baseErr := out.result, out.err
	r.origResult, r.origErr = base, baseErr
	if out.adopted != nil || out.ambiguous != nil {
		return r.finish(out)
	}
	if baseErr != nil && out.pending == nil {
		return r.result, baseErr
	}
	if baseErr != nil {
		// A missing secret ended the first step. Judging whether that absence
		// is reliable, and continuing past it, is new work.
		if r.req.From != "" {
			return r.result, baseErr
		}
		if !r.req.SchemeSupplied {
			if reason := r.preloadStored(); reason != "" {
				return r.result, baseErr
			}
		}
		if r.expired() || !r.reliable(out.pending.lookup) {
			return r.result, baseErr
		}
		out.pending.ev = evidence{state: unusable, settled: true}
		cont := r.evaluateFrom(first, out.pendingIndex+1, continuation, out)
		if cont.err != nil || cont.abstain != "" {
			return r.result, baseErr
		}
		if cont.adopted != nil || cont.ambiguous != nil {
			return r.finish(cont)
		}
		// The absence was reliable: that error becomes a normal no-change result,
		// unless the rest of the new work fails.
		improved := r.noChange("no matching stored identity can be reused")
		r.improved = &improved
		out = cont
	}
	if r.req.SchemeSupplied || r.req.From != "" {
		if r.improved != nil {
			return *r.improved, nil
		}
		return base, nil
	}
	return r.settle(out)
}

// settle runs the later steps after the first one and returns the better of
// their conclusion and the baseline.
func (r *run) settle(first outcome) (Result, error) {
	fallback := r.giveUp
	if first.abstain != "" {
		return fallback(first.abstain)
	}
	if first.verifiedTarget {
		return r.unchangedVerified(), nil
	}
	if first.adopted != nil || first.ambiguous != nil {
		return r.finish(first)
	}
	// Stored secrets are looked up before judging the first step, so a keychain
	// answer among them can serve as the witness for its evidence.
	if reason := r.preloadStored(); reason != "" {
		return fallback(reason)
	}
	if !r.allSure(first.cands) {
		return fallback("")
	}
	r.earlier = append(r.earlier, first.cands...)
	for _, session := range []bool{false, true} {
		cands, reason := r.laterCandidates(session)
		if reason != "" {
			return fallback(reason)
		}
		if len(cands) == 0 {
			continue
		}
		if !r.targetChecked {
			if reason := r.checkTarget(true); reason != "" {
				return fallback(reason)
			}
			if r.targetEv.state == usable {
				return r.unchangedVerified(), nil
			}
		}
		out := r.evaluate(cands, later)
		if out.abstain != "" {
			return fallback(out.abstain)
		}
		if out.adopted != nil || out.ambiguous != nil {
			return r.finish(out)
		}
		if !r.allSure(out.cands) {
			return fallback("")
		}
		r.earlier = append(r.earlier, out.cands...)
	}
	if r.labelled && r.targetEv.state == unusable && r.sure(r.targetEv) {
		return r.noChange("no matching stored identity can be reused"), nil
	}
	return fallback("")
}

// giveUp returns the earlier answer when new work cannot reach a better
// conclusion: its error, or its result with the cause. Work that finished
// without a failure keeps a conclusion it already reached.
func (r *run) giveUp(reason string) (Result, error) {
	r.abstained = r.abstained || reason != ""
	if reason == "" && r.improved != nil {
		return *r.improved, nil
	}
	if r.origErr != nil {
		return r.result, r.origErr
	}
	res := r.origResult
	if reason != "" {
		res.Reason = reason
	}
	return res, nil
}

// allSure reports whether every source is reliably unusable now.
func (r *run) allSure(cands []*candidate) bool {
	for _, c := range cands {
		if c.ev.state != unusable || !r.sure(c.ev) {
			return false
		}
	}
	return true
}

// explicit handles --from-scheme and a --from-context of another scheme.
func (r *run) explicit(from Context) (Result, error) {
	var specs []Spec
	if r.req.FromScheme == "" {
		specs = []Spec{contextSpec(from)}
	} else {
		if !r.provenance() {
			return r.result, r.sourceMismatch("stored credentials for this host may belong to another service or scope")
		}
		s := r.req.FromScheme
		if s == r.target.Scheme && !auth.UsesUsername(s) {
			return r.result, r.sourceMismatch("the selected scheme is the destination's own credential")
		}
		var names []string
		switch {
		case !auth.UsesUsername(s):
			names = []string{""}
		case r.req.From != "":
			if from.Username == "" {
				return r.result, cerrors.New(cerrors.CategoryNotFound, "AUTH_REUSE_SOURCE_NOT_FOUND", "the selected context records no username to pair with the stored credential").
					WithNextSteps(config.ListContextsStep())
			}
			names = []string{from.Username}
		case r.labelled:
			names = []string{r.target.Username}
		default:
			names = r.serviceUsernames()
		}
		if len(names) == 0 {
			return r.result, cerrors.New(cerrors.CategoryNotFound, "AUTH_REUSE_SOURCE_NOT_FOUND", "no username recorded for this service pairs with the stored credential").
				WithNextSteps(config.ListContextsStep())
		}
		for _, name := range names {
			specs = append(specs, Spec{Scheme: s, Username: name, KeyBase: r.target.KeyBase})
		}
	}
	var verified []*candidate
	var rejection error
	for _, spec := range specs {
		if !r.req.Valid(spec.Scheme) {
			return r.result, r.sourceMismatch("the destination would not be a valid configuration under the source's scheme")
		}
		if r.labelled && auth.UsesUsername(spec.Scheme) && !r.req.SameUsername(spec.Username, r.target.Username) {
			return r.result, r.sourceMismatch("the source's username differs from the one this context records")
		}
		c := &candidate{spec: spec}
		if spec.Context != "" {
			cred, l, err := r.req.Resolve(spec)
			r.observe(l)
			if err != nil {
				return r.result, err
			}
			c.cred, c.lookup = cred, l
		} else {
			l := r.req.Lookup(r.req.Key(spec.KeyBase, spec.Scheme))
			r.observe(l)
			if err := l.Err(); err != nil {
				return r.result, storeError(err)
			}
			c.lookup, c.cred = l, auth.Credential{Scheme: spec.Scheme, Username: spec.Username, Secret: l.Secret}
			if err := c.cred.Validate(); err != nil {
				return r.result, err
			}
		}
		account, err := r.req.Verify(context.Background(), spec, c.cred)
		switch {
		case err == nil:
			c.account, c.ev = account, evidence{state: usable}
			verified = append(verified, c)
		case rejected(err) && len(specs) > 1:
			// One of several pairings; another may be the right username.
			rejection = err
		default:
			return r.result, err
		}
	}
	if len(verified) == 0 {
		return r.result, rejection
	}
	verified = dedupe(r, verified)
	if len(verified) > 1 {
		return r.result, r.ambiguous(verified)
	}
	chosen := verified[0]
	if auth.IsSession(r.target.Scheme) && !auth.IsSession(chosen.spec.Scheme) {
		return r.adopt(chosen, false)
	}
	if reason := r.checkTarget(false); reason != "" {
		return r.result, r.targetEv.err
	}
	switch {
	case r.targetEv.state == usable:
		return r.unchangedVerified(), nil
	case r.sure(r.targetEv):
		return r.adopt(chosen, false)
	case isMissing(r.targetEv.err):
		return r.result, r.targetEv.err
	}
	return r.result, unreliableError(r.targetLookup)
}

// outcome is the result of evaluating one step's sources.
type outcome struct {
	result         Result
	err            error
	abstain        string // new work could not reach a conclusion; why
	pending        *candidate
	pendingIndex   int
	adopted        *candidate
	ambiguous      []*candidate
	verifiedTarget bool
	newWork        bool
	cands          []*candidate
	matches        []*candidate
}

// firstStep lists the sources the first step looked at before, in name order:
// contexts on the service under the destination's scheme, except the ones
// that resolve its own credential without a label of their own.
func (r *run) firstStep() []Context {
	var out []Context
	for _, c := range r.peers() {
		if c.Scheme != r.target.Scheme || (r.req.From != "" && !strings.EqualFold(c.Name, r.req.From)) {
			continue
		}
		if c.Username == "" && r.sameKey(c, c.Scheme) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// mode says how a walk over sources treats what it finds.
type mode int

const (
	// baseline keeps the earlier errors and adopts as before.
	baseline mode = iota
	// continuation is new work past a reliably absent source: an absence it
	// cannot judge keeps the earlier error.
	continuation
	// later is new work over sources the command did not look at before: an
	// absent secret is simply not a login.
	later
)

func (r *run) evaluate(list []Context, m mode) outcome {
	return r.evaluateFrom(list, 0, m, outcome{})
}

// evaluateFrom walks the first step from index start. In the baseline it keeps
// the earlier errors; as new work it never fails and adopts only on reliable
// evidence.
func (r *run) evaluateFrom(list []Context, start int, m mode, prior outcome) outcome {
	newWork := m != baseline
	out := outcome{cands: prior.cands, matches: prior.matches, newWork: newWork}
	if prior.pending != nil {
		out.cands = append(out.cands, prior.pending)
	}
	if start == 0 && len(list) > 0 {
		if r.labelled && !r.hasOwnSources(list) {
			return out
		}
		if reason := r.checkTarget(newWork); reason != "" {
			if newWork {
				out.abstain = reason
			} else {
				out.err = r.targetEv.err
			}
			return out
		}
		if r.targetEv.state == usable {
			out.verifiedTarget = true
			out.result = r.unchangedVerified()
			return out
		}
	}
	for i := start; i < len(list); i++ {
		if newWork && r.expired() {
			out.abstain = "the time allowed for checking other logins ran out"
			return out
		}
		c := list[i]
		cand := &candidate{spec: contextSpec(c)}
		out.cands = append(out.cands, cand)
		switch {
		case auth.UsesUsername(c.Scheme) && c.Username == "":
			// It has no login of its own; no lookup is needed to know that.
			cand.ev = evidence{state: unusable, settled: true}
			continue
		case !auth.UsesUsername(c.Scheme) && r.sameKey(c, c.Scheme):
			// A labelled context on the destination's own secret: the
			// destination's lookup stands in for its own.
			if r.labelled {
				cand.ev = evidence{state: unusable, settled: true}
				continue
			}
			if r.targetLookup == nil {
				cand.ev = r.targetEv
				continue
			}
			cand.lookup, cand.ev = *r.targetLookup, r.targetEv
			if isMissing(r.targetEv.err) {
				cand.ev.err = r.targetEv.err
				if newWork && !r.reliable(*r.targetLookup) {
					out.abstain = "the destination's stored secret is not visible here"
					return out
				}
				if !newWork {
					out.err, out.pending, out.pendingIndex = r.targetEv.err, cand, i
					out.cands = out.cands[:len(out.cands)-1]
					return out
				}
			}
			continue
		}
		cred, l, err, ok := r.resolve(cand.spec, newWork)
		if !ok {
			out.abstain = "the time allowed for checking other logins ran out"
			return out
		}
		cand.lookup = l
		if err != nil {
			code := cerrors.AsCLIError(err).Code
			switch {
			case skippable(code):
				cand.ev = evidence{state: unusable, looked: true, lookup: l}
				continue
			case code == "CREDENTIAL_NOT_VISIBLE_OR_MISSING":
				if m == continuation && !r.reliable(l) {
					out.abstain = "a stored login is not visible here"
					return out
				}
				if newWork {
					cand.ev = evidence{state: unusable, looked: true, lookup: l}
					continue
				}
				cand.ev.err = err
				out.err, out.pending, out.pendingIndex = err, cand, i
				out.cands = out.cands[:len(out.cands)-1]
				return out
			default:
				if newWork {
					out.abstain = "a stored login could not be read here"
					return out
				}
				out.err = err
				return out
			}
		}
		cand.cred = cred
		account, verr := r.verify(cand.spec, cred, newWork)
		switch {
		case verr == nil:
			cand.account, cand.ev = account, evidence{state: usable}
			out.matches = append(out.matches, cand)
		case rejected(verr):
			cand.ev = evidence{state: unusable, looked: true, lookup: l}
		case newWork:
			out.abstain = "checking another login failed: " + cerrors.AsCLIError(verr).Message
			return out
		default:
			out.err = verr
			return out
		}
	}
	return r.conclude(out, newWork)
}

// conclude turns the verified matches of a step into an adoption, an
// ambiguity or nothing.
func (r *run) conclude(out outcome, newWork bool) outcome {
	matches := dedupe(r, out.matches)
	if len(matches) == 0 {
		out.result = r.noChange("no matching stored identity can be reused")
		return out
	}
	if newWork && !r.evidenceFor(out.cands, matches) {
		out.abstain = "the credential store view cannot confirm that this context has no login of its own"
		return out
	}
	if len(matches) > 1 {
		out.ambiguous = matches
		return out
	}
	out.adopted = matches[0]
	return out
}

// laterCandidates lists the sources of the second (long-lived) or third
// (browser session) step.
func (r *run) laterCandidates(session bool) ([]Context, string) {
	var list []Context
	// A credential an earlier step already looked at, or the destination's
	// own, is not tried again: a rejected password must not count twice
	// toward a lockout.
	seen := map[string]bool{r.credentialID(contextSpec(r.target)): true}
	for _, c := range r.earlier {
		seen[r.credentialID(c.spec)] = true
	}
	add := func(c Context) {
		id := r.credentialID(Spec{Scheme: c.Scheme, Username: c.Username, KeyBase: c.KeyBase})
		if seen[id] {
			return
		}
		seen[id] = true
		list = append(list, c)
	}
	for _, c := range r.peers() {
		if c.Scheme == r.target.Scheme || !r.eligible(c.Scheme, session) || !r.labelMatches(c) {
			continue
		}
		if auth.UsesUsername(c.Scheme) && c.Username == "" {
			continue
		}
		if !auth.UsesUsername(c.Scheme) && r.sameKey(c, c.Scheme) {
			continue
		}
		add(c)
	}
	if r.provenance() {
		for _, s := range auth.SchemeNames() {
			if !r.eligible(s, session) || (s == r.target.Scheme && !auth.UsesUsername(s)) {
				continue
			}
			// Only a secret that exists is a source. A lookup that cannot
			// answer might hide one, so the step gives up rather than choose.
			l, ok := r.lookupWithin(r.req.Key(r.target.KeyBase, s))
			if !ok {
				return nil, "the time allowed for checking other logins ran out"
			}
			r.observe(l)
			if err := l.Err(); err != nil {
				if errors.Is(err, credstore.ErrSecretNotFound) {
					continue
				}
				return nil, "a stored login could not be read here"
			}
			names := []string{""}
			if auth.UsesUsername(s) {
				names = r.serviceUsernames()
			}
			for _, name := range names {
				c := Context{Scheme: s, Username: name, KeyBase: r.target.KeyBase, Service: r.target.Service}
				if r.labelMatches(c) {
					add(c)
				}
			}
		}
	}
	return list, ""
}

func (r *run) eligible(scheme string, session bool) bool {
	return auth.NeedsCredential(scheme) && auth.IsSession(scheme) == session && r.req.Valid(scheme)
}

func (r *run) labelMatches(c Context) bool {
	return !r.labelled || !auth.UsesUsername(c.Scheme) || r.req.SameUsername(c.Username, r.target.Username)
}

// preloadStored looks up the destination service's stored secrets once, so
// that a keychain answer can serve as a witness before evidence is judged. It
// returns a reason when the deadline passed first.
func (r *run) preloadStored() string {
	if r.preloaded || !r.provenance() {
		return ""
	}
	r.preloaded = true
	for _, s := range auth.SchemeNames() {
		if !auth.NeedsCredential(s) {
			continue
		}
		l, ok := r.lookupWithin(r.req.Key(r.target.KeyBase, s))
		if !ok {
			return "the time allowed for checking other logins ran out"
		}
		r.observe(l)
	}
	return ""
}

// checkTarget resolves and verifies the destination's own credential once. It
// returns a reason when the check could not reach a conclusion.
func (r *run) checkTarget(newWork bool) string {
	if r.targetChecked {
		return ""
	}
	r.targetChecked = true
	if r.usesUser && r.target.Username == "" {
		r.targetEv = evidence{state: unusable, settled: true}
		return ""
	}
	spec := contextSpec(r.target)
	cred, l, err, ok := r.resolve(spec, newWork)
	if !ok {
		r.targetChecked = false
		return "the time allowed for checking other logins ran out"
	}
	r.targetLookup = &l
	if err != nil {
		code := cerrors.AsCLIError(err).Code
		switch {
		case skippable(code):
			r.targetEv = evidence{state: unusable, looked: true, lookup: l, err: err}
			return ""
		case code == "CREDENTIAL_NOT_VISIBLE_OR_MISSING":
			r.targetEv = evidence{state: unusable, looked: true, lookup: l, err: err}
			return ""
		}
		r.targetEv = evidence{err: err}
		return "this context's own login could not be read here"
	}
	_, verr := r.verify(spec, cred, newWork)
	switch {
	case verr == nil:
		r.targetEv = evidence{state: usable}
	case rejected(verr):
		r.targetEv = evidence{state: unusable, looked: true, lookup: l, err: verr}
	default:
		r.targetEv = evidence{err: verr}
		return "checking this context's own login failed: " + cerrors.AsCLIError(verr).Message
	}
	return ""
}

// evidenceFor reports whether reliable evidence shows that the destination and
// every source passed over have no working login.
func (r *run) evidenceFor(cands, matches []*candidate) bool {
	if !r.targetChecked || r.targetEv.state != unusable || !r.sure(r.targetEv) {
		return false
	}
	chosen := map[*candidate]bool{}
	for _, m := range matches {
		chosen[m] = true
	}
	for _, c := range append(append([]*candidate(nil), r.earlier...), cands...) {
		if chosen[c] || c.ev.state == usable {
			continue
		}
		if c.ev.state != unusable || !r.sure(c.ev) {
			return false
		}
	}
	return true
}

func (r *run) finish(out outcome) (Result, error) {
	if out.ambiguous != nil {
		return r.result, r.ambiguous(out.ambiguous)
	}
	return r.adopt(out.adopted, out.newWork)
}

// adopt points the destination at the chosen source, after rechecking that
// nothing it relied on changed.
func (r *run) adopt(c *candidate, newWork bool) (Result, error) {
	u := Update{Scheme: c.spec.Scheme}
	if auth.UsesUsername(c.spec.Scheme) {
		u.Username, u.SetUsername = c.spec.Username, c.spec.Username != r.target.Username
	}
	if r.req.Key(r.target.KeyBase, c.spec.Scheme) != r.req.Key(c.spec.KeyBase, c.spec.Scheme) {
		u.BaseURL = c.spec.KeyBase
	}
	res := r.result
	res.SourceContext = c.spec.Context
	res.Scheme = c.spec.Scheme
	if auth.UsesUsername(c.spec.Scheme) {
		res.Username = c.spec.Username
	}
	res.Account, res.AccountName = c.account.ID, c.account.Name
	res.Verified = true
	if c.spec.Context == "" {
		res.Reason = "using the " + c.spec.Scheme + " credential stored for this service"
	}
	res.Changed = u.Scheme != r.target.Scheme || u.SetUsername || u.BaseURL != ""
	if !res.Changed {
		res.State = StateUnchanged
		return res, nil
	}
	res.State = StateAvailable
	if r.req.DryRun {
		return res, nil
	}
	switch unchanged, ok := r.unchangedSource(c, newWork); {
	case !ok:
		// New work stays within its deadline, rechecks included.
		return r.giveUp("the time allowed for checking other logins ran out")
	case !unchanged:
		return res, cerrors.New(cerrors.CategoryConflict, "AUTH_REUSE_CREDENTIAL_CHANGED", "stored credential changed during verification; no identity was saved").
			WithNextSteps(r.retry())
	}
	if err := r.req.Persist(u); err != nil {
		switch {
		case errors.Is(err, ErrConfigChanged):
			return res, cerrors.New(cerrors.CategoryConflict, "AUTH_REUSE_CONFIG_CHANGED", "configuration changed during verification; no identity was saved").
				WithNextSteps(r.retry())
		case isCLIError(err):
			return res, err
		default:
			return res, cerrors.Wrap(err, cerrors.CategoryConfig, "AUTH_REUSE_WRITE_FAILED", "verified identity could not be saved; credentials were not changed")
		}
	}
	res.State = StateReused
	return res, nil
}

// unchangedSource rechecks the chosen source credential and, when it was
// looked up, the destination's own entry. ok is false when new work ran out
// of time before the recheck finished.
func (r *run) unchangedSource(c *candidate, newWork bool) (unchanged, ok bool) {
	cred, _, err, ok := r.resolve(c.spec, newWork)
	if !ok {
		return false, false
	}
	if err != nil || cred != c.cred {
		return false, true
	}
	if r.targetLookup != nil {
		key := r.req.Key(r.target.KeyBase, r.target.Scheme)
		l, ok := r.req.Lookup(key), true
		if newWork {
			l, ok = r.lookupWithin(key)
		}
		if !ok {
			return false, false
		}
		before := *r.targetLookup
		if l.Found() != before.Found() || l.Secret != before.Secret || l.Keychain != before.Keychain || l.File != before.File {
			return false, true
		}
	}
	if newWork && r.expired() {
		return false, false
	}
	return true, true
}

func (r *run) ambiguous(cands []*candidate) error {
	names := []string{}
	list := []Candidate{}
	for _, c := range cands {
		if c.spec.Context != "" {
			names = append(names, c.spec.Context)
		}
		item := Candidate{Context: c.spec.Context, Scheme: c.spec.Scheme, Account: c.account.ID}
		if auth.UsesUsername(c.spec.Scheme) {
			item.Username = c.spec.Username
		}
		list = append(list, item)
	}
	// One complete command per candidate, so choosing is following a step.
	steps := []string{config.ListContextsStep()}
	for _, c := range cands {
		switch {
		case c.spec.Context != "":
			steps = append(steps, r.selected()+" auth reuse --from-context "+config.QuoteArg(c.spec.Context)+" --dry-run")
		case !auth.UsesUsername(c.spec.Scheme):
			steps = append(steps, r.selected()+" auth reuse --from-scheme "+c.spec.Scheme+" --dry-run")
		default:
			if holder := r.usernameHolder(c.spec.Username); holder != "" {
				steps = append(steps, r.selected()+" auth reuse --from-scheme "+c.spec.Scheme+" --from-context "+config.QuoteArg(holder)+" --dry-run")
			}
		}
	}
	return cerrors.New(cerrors.CategoryConflict, "AUTH_REUSE_AMBIGUOUS", "multiple matching login identities are available; choose a source").
		WithDetails(map[string]any{"contexts": names, "candidates": list}).
		WithNextSteps(steps...)
}

// usernameHolder names a context on the service that records username, the
// destination first.
func (r *run) usernameHolder(username string) string {
	if r.target.Username != "" && r.req.SameUsername(r.target.Username, username) {
		return r.target.Name
	}
	for _, c := range r.peers() {
		if c.Username != "" && r.req.SameUsername(c.Username, username) {
			return c.Name
		}
	}
	return ""
}

func (r *run) unchanged(reason string) Result {
	res := r.result
	res.State, res.Reason = StateUnchanged, reason
	return res
}

func (r *run) unchangedVerified() Result {
	res := r.unchanged("destination already has a verified login")
	res.Verified = true
	return res
}

// noChange is the result when nothing was adopted: unavailable, or unchanged
// for a labelled destination whose own login was not shown to fail.
func (r *run) noChange(reason string) Result {
	if r.labelled && !(r.targetChecked && r.targetEv.state == unusable && r.sure(r.targetEv)) {
		return r.unchanged("destination identity is already configured")
	}
	res := r.result
	res.State, res.Reason = StateUnavailable, reason
	return res
}

func (r *run) nextSteps(res Result) []string {
	check := r.selected() + " " + r.req.Check
	switch res.State {
	case StateReused, StateUnchanged:
		return []string{check}
	case StateAvailable:
		return []string{r.reuseCommand()}
	}
	var steps []string
	if r.abstained || r.storeHidden {
		steps = append(steps, "Retry `"+r.selected()+" auth reuse` with access to the host user environment, or later.")
	}
	if !(r.usesUser && r.target.Username == "") {
		steps = append(steps, check)
	}
	login := r.words.AuthLogin
	if r.req.LoginStep != nil {
		login = r.req.LoginStep(r.target.Scheme)
	}
	return append(steps, r.selected()+" "+r.words.AuthGuide, r.selected()+" "+login)
}

// provenance reports whether the destination's credential key can only belong
// to its service: every context sharing the key's host part is that service.
func (r *run) provenance() bool {
	prefix := r.req.Key(r.target.KeyBase, "")
	for _, c := range r.req.Contexts {
		if r.req.Key(c.KeyBase, "") == prefix && c.Service != r.target.Service {
			return false
		}
	}
	return true
}

// serviceUsernames lists the distinct usernames recorded on the service's
// contexts, labels included, the destination's first.
func (r *run) serviceUsernames() []string {
	var names []string
	add := func(u string) {
		if u == "" {
			return
		}
		for _, n := range names {
			if r.req.SameUsername(n, u) {
				return
			}
		}
		names = append(names, u)
	}
	add(r.target.Username)
	for _, c := range r.peers() {
		add(c.Username)
	}
	return names
}

func (r *run) peers() []Context {
	var out []Context
	for _, c := range r.req.Contexts {
		if c.Service == r.target.Service && !strings.EqualFold(c.Name, r.target.Name) {
			out = append(out, c)
		}
	}
	sort.SliceStable(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func (r *run) hasOwnSources(list []Context) bool {
	for _, c := range list {
		if auth.UsesUsername(c.Scheme) && c.Username == "" {
			continue
		}
		if !auth.UsesUsername(c.Scheme) && r.sameKey(c, c.Scheme) {
			continue
		}
		return true
	}
	return false
}

func (r *run) sameKey(c Context, scheme string) bool {
	return r.req.Key(c.KeyBase, scheme) == r.req.Key(r.target.KeyBase, r.target.Scheme)
}

func (r *run) credentialID(s Spec) string {
	id := r.req.Key(s.KeyBase, s.Scheme)
	if auth.UsesUsername(s.Scheme) {
		id += "\x00" + s.Username
	}
	return id
}

func (r *run) verify(spec Spec, cred auth.Credential, newWork bool) (Account, error) {
	ctx := context.Background()
	if newWork {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, r.deadline)
		defer cancel()
	}
	return r.req.Verify(ctx, spec, cred)
}

// resolve resolves spec; as new work it gives up when the deadline passes.
func (r *run) resolve(spec Spec, newWork bool) (auth.Credential, credstore.Lookup, error, bool) {
	type answer struct {
		cred auth.Credential
		l    credstore.Lookup
		err  error
	}
	if !newWork {
		cred, l, err := r.req.Resolve(spec)
		r.observe(l)
		return cred, l, err, true
	}
	done := make(chan answer, 1)
	go func() {
		cred, l, err := r.req.Resolve(spec)
		done <- answer{cred, l, err}
	}()
	wait := time.Until(r.deadline)
	if wait <= 0 {
		return auth.Credential{}, credstore.Lookup{}, nil, false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case a := <-done:
		r.observe(a.l)
		return a.cred, a.l, a.err, true
	case <-timer.C:
		return auth.Credential{}, credstore.Lookup{}, nil, false
	}
}

// lookupWithin reads a stored secret unless the deadline passes first.
func (r *run) lookupWithin(key string) (credstore.Lookup, bool) {
	done := make(chan credstore.Lookup, 1)
	go func() { done <- r.req.Lookup(key) }()
	wait := time.Until(r.deadline)
	if wait <= 0 {
		return credstore.Lookup{}, false
	}
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case l := <-done:
		return l, true
	case <-timer.C:
		return credstore.Lookup{}, false
	}
}

func (r *run) expired() bool { return !r.req.Now().Before(r.deadline) }

func (r *run) observe(l credstore.Lookup) {
	switch l.Keychain {
	case credstore.Hit:
		r.witness = true
	case credstore.Failed, credstore.Skipped:
		r.storeHidden = true
	}
}

// sure reports whether evidence is reliable now.
func (r *run) sure(ev evidence) bool {
	return ev.settled || (ev.looked && r.reliable(ev.lookup))
}

// reliable reports whether a lookup is reliable evidence: the keychain itself
// answered, and returned a secret of this CLI during this run.
func (r *run) reliable(l credstore.Lookup) bool {
	return l.Keychain == credstore.Hit || (l.Keychain == credstore.Miss && r.witness)
}

func (r *run) selected() string {
	cmd := r.words.App + " " + r.words.ContextFlag + " " + config.QuoteArg(r.target.Name)
	if r.req.SchemeFlag != "" {
		cmd += " --auth-scheme " + r.req.SchemeFlag
	}
	return cmd
}

func (r *run) step(command string) string { return r.words.App + " " + command }

// reuseCommand repeats this invocation's selection without --dry-run.
func (r *run) reuseCommand() string {
	cmd := r.selected() + " auth reuse"
	if r.req.From != "" {
		cmd += " --from-context " + config.QuoteArg(r.req.From)
	}
	if r.req.FromScheme != "" {
		cmd += " --from-scheme " + r.req.FromScheme
	}
	return cmd
}

// retry previews this invocation's selection again.
func (r *run) retry() string { return r.reuseCommand() + " --dry-run" }

func (r *run) sourceMismatch(message string) error {
	return cerrors.New(cerrors.CategoryConflict, "AUTH_REUSE_SOURCE_MISMATCH", message).
		WithNextSteps(config.ListContextsStep())
}

func dedupe(r *run, cands []*candidate) []*candidate {
	sort.SliceStable(cands, func(i, j int) bool { return rank(r, cands[i]) < rank(r, cands[j]) })
	var out []*candidate
	seen := map[string]bool{}
	for _, c := range cands {
		id := r.credentialID(c.spec)
		if c.account.ID != "" {
			id = "account\x00" + c.account.ID
		}
		if seen[id] {
			continue
		}
		seen[id] = true
		out = append(out, c)
	}
	return out
}

// rank orders candidates of one account: the destination's scheme first, then
// a context before a stored secret, then by name.
func rank(r *run, c *candidate) string {
	key := "1"
	if c.spec.Scheme == r.target.Scheme {
		key = "0"
	}
	if c.spec.Context == "" {
		return key + "1"
	}
	return key + "0" + c.spec.Context
}

func find(contexts []Context, name string) (Context, bool) {
	for _, c := range contexts {
		if strings.EqualFold(c.Name, name) {
			return c, true
		}
	}
	return Context{}, false
}

func contextSpec(c Context) Spec {
	return Spec{Scheme: c.Scheme, Username: c.Username, KeyBase: c.KeyBase, Context: c.Name}
}

func skippable(code string) bool {
	return code == "AUTH_NO_TOKEN" || code == "AUTH_NO_BASIC" || code == "AUTH_NO_BEARER" || code == "AUTH_BAD_SESSION"
}

func isMissing(err error) bool {
	return err != nil && cerrors.AsCLIError(err).Code == "CREDENTIAL_NOT_VISIBLE_OR_MISSING"
}

func rejected(err error) bool {
	ce := cerrors.AsCLIError(err)
	return ce.Category == cerrors.CategoryAuth && ce.HTTPStatus == 401
}

func isCLIError(err error) bool {
	var ce *cerrors.CLIError
	return errors.As(err, &ce)
}

func storeError(err error) error {
	if errors.Is(err, credstore.ErrSecretNotFound) {
		return auth.MissingError(nil)
	}
	return auth.InaccessibleError(err)
}

func unreliableError(l *credstore.Lookup) error {
	if l != nil && l.KeychainErr != nil {
		return auth.InaccessibleError(&credstore.StoreAccessError{Backend: credstore.BackendKeychain, Err: l.KeychainErr})
	}
	return auth.MissingError(nil)
}

// MayReuse decides offline, from the configuration file alone, whether
// `auth reuse` could find a source for target: the context has no identity,
// stored or supplied at run time, and either the CLI accepts another scheme
// (whose stored secret only a lookup could find) or another context on the
// service holds a login of its own under the destination's scheme. With a
// supplied scheme only the latter counts. runtimeUsername is a username the
// invocation supplied; valid reports whether the destination stays a valid
// configuration under a scheme (nil accepts every scheme).
func MayReuse(target Context, contexts []Context, runtimeUsername string, schemeSupplied bool, valid func(scheme string) bool) bool {
	if target.Service == "" || !auth.NeedsCredential(target.Scheme) || runtimeUsername != "" {
		return false
	}
	if auth.UsesUsername(target.Scheme) && target.Username != "" {
		return false
	}
	if valid == nil {
		valid = func(string) bool { return true }
	}
	if !schemeSupplied {
		for _, s := range auth.SchemeNames() {
			if s != target.Scheme && auth.NeedsCredential(s) && valid(s) {
				return true
			}
		}
		if auth.UsesUsername(target.Scheme) {
			// A stored secret under the destination's own scheme pairs with any
			// username recorded on the service.
			for _, c := range contexts {
				if c.Service == target.Service && !strings.EqualFold(c.Name, target.Name) && c.Username != "" {
					return true
				}
			}
		}
	}
	for _, c := range contexts {
		if c.Service != target.Service || strings.EqualFold(c.Name, target.Name) || c.Scheme != target.Scheme {
			continue
		}
		if auth.UsesUsername(c.Scheme) && c.Username == "" {
			continue
		}
		if !auth.UsesUsername(c.Scheme) && c.KeyBase == target.KeyBase {
			continue
		}
		return true
	}
	return false
}
