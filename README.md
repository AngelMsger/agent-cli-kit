# agent-cli-kit

Shared Go packages for the AngelMsger CLI family: jira-cli, confluence-cli,
bitbucket-cli, jenkins-cli, prometheus-cli, openobserve-cli and
wecom-calendar-cli. Each CLI used to carry its own copy of these packages, and
the copies drifted. The kit keeps one copy, so a fix such as the macOS keychain
probe lands once.

The kit holds code only. The family's principles and rules for building CLIs
that work well for both people and agents live in
[agent-clis](https://github.com/AngelMsger/agent-clis).

```sh
go get github.com/angelmsger/agent-cli-kit@latest
```

## Packages

| Package | What it does |
|---|---|
| `errors` | Categorized `CLIError`, exit codes, HTTP status mapping and the JSON error envelope agents parse. |
| `output` | JSON and table rendering, `--fields` projection, the list envelope with pagination and `_notice`. |
| `transport` | HTTP client with decorators, bounded retries that replay the request body, and optional `--verbose` logging. |
| `credstore` | Secret storage in the OS keychain with a protected file fallback. Probes macOS for a default keychain first. |
| `auth` | Auth schemes, the `Authorization` header, keychain account keys, and `Resolve`, which finds a secret and returns recovery errors an agent can act on when the sandbox cannot see it. |
| `config` | Layered settings (flags, environment, `.env`, a YAML file of named contexts, defaults) with the layer that supplied each value, context selection, the atomic file write, team presets (`set-context`), and the context hints. |
| `cliflags` | Corrects common agent mistakes in argv (`--flag value` vs `--flag=value`, literal `\n` in body flags). |
| `update` | Cached check against a repository's latest GitHub release. |
| `skill` | Read-only Skill version, load-state and installation inspection over caller-supplied `fs.FS`, plus agent integration directory metadata. |

## What each CLI supplies

The kit has no business CLI names, environment variables or commands in its implementation. Shared Skill integration IDs (such as `codex`) identify supported agent directories; they do not select product behavior. A CLI passes its product settings in once, at startup:

```go
// Product hints and next steps for each error category.
errors.SetGuidance(hints.Guidance)

client := transport.New(transport.Options{
	UserAgent:  "jenkins-cli/" + constants.Version,
	MaxRetries: 2,
	Verbose:    verboseWriter,
})

store := credstore.NewStore(configDir, "jenkins-cli")

// Auth schemes and the product wording of the recovery steps.
auth.Register(&auth.Product{
	Schemes: []auth.Scheme{
		{Name: "token", Kind: auth.Basic, MissingCode: "AUTH_NO_TOKEN",
			Missing: "token auth requires both a username and an API token"},
		{Name: "basic", Kind: auth.Basic, Missing: "basic auth requires both a username and a password"},
	},
	DefaultScheme:    "token",
	Name:             "Jenkins",
	Doctor:           "jenkins-cli doctor",
	MissingStep:      "Only if the host retry also reports missing credentials, run `jenkins-cli config init` in the user's terminal or set JENKINS_* environment variables.",
	InaccessibleStep: "Do not run `config init` unless the same check also fails in the host environment.",
	ListSchemes:      true,
})
cred, err := auth.Resolve(auth.Request{
	Scheme:   cfg.Auth.Scheme,
	Username: cfg.Auth.Username,
	Secrets:  map[string]string{"token": secrets.Token, "basic": secrets.Password},
	BaseURL:  cfg.BaseURL,
}, store)

// Settings: one row per field, the file location and the command wording.
// The CLI keeps its own structs, tagged `config:"<key>"`, and its YAML shape.
config.Register(&config.Schema{
	Fields: []config.Field{
		{Key: config.KeyServer, Env: []string{"JENKINS_URL"}, Service: true},
		{Key: config.KeyAuthScheme, Env: []string{"JENKINS_AUTH_SCHEME"}, Default: "token", Service: true},
		{Key: "auth.username", Env: []string{"JENKINS_USER"}},
		{Key: "defaults.timeout", Default: "30s"},
		{Key: "secret.token", Env: []string{"JENKINS_TOKEN"}, Implies: "token"},
	},
	Paths: config.Paths{Dir: ".angelmsger/jenkins", File: "config.yaml"},
	Words: config.Words{App: "jenkins-cli", ContextFlag: "--use-context", ContextEnv: "JENKINS_CONTEXT",
		NoHintEnv: "JENKINS_CLI_NO_CONTEXT_HINT", ListContexts: "config get-contexts",
		UseContext: "config use-context", SetContext: "config set-context",
		AuthGuide: "auth guide", AuthLogin: "auth login"},
	Schemes: []string{"token", "basic"},
	URL:     config.URLRules{AssumeScheme: "http"},
})
var shape fileShape // the CLI's own YAML document type
found, err := config.ReadFile(dir, &shape)
file := shape.file() // a config.File[NamedContext, Defaults]
resolved, err := config.Load(file, config.LoadOptions{Flags: config.Encode(flags), Context: useContext})
config.Decode(resolved.Values, &cfg)

checker := update.Checker{Repo: "AngelMsger/jenkins-cli", EndpointEnv: "JENKINS_RELEASE_API"}
status := checker.Cached(ctx, client, cacheDir, constants.Version)
```

## Output integration

For paginated NDJSON, `output.EmitList` writes item rows first, then emits one
compact `_notice.pagination` record only if more results remain and every row
write succeeded. Empty filtered pages and field projections retain continuation;
completed pages and stdout failures emit no pagination notice. JSON envelopes
remain unchanged. `Options.NoticeWriter` defaults to stderr;
`Options.NextFlag` defaults to `--cursor` and controls both the NDJSON hint and
table footer. Commands supply their actual cursor/offset and continuation flag.
The family [wire contract](https://github.com/AngelMsger/agent-clis/blob/main/docs/agent-facing-cli-best-practices.md#42-use-stable-envelopes)
defines observable behavior; package tests pin serialization and ordering.

## Skill read core

`skill.Version`, `VersionsEqual`, `Loaded`, `LoadState`, `Inspect` and
`Installation` inspect caller-supplied data and filesystems. `Agent`, `Agents`,
`AgentByID`, `AgentIDs`, `Detect` and `Agent.Destination` provide the shared agent
integration directory table and destination calculation. The package does not
read environment variables, discover home/current directories, print output,
construct Cobra commands or write/remove files.

Each CLI owns its embedded filesystem/root/name, environment lookup, output
shape and wording, notice timing, and install/uninstall mutation policy. This
split preserves the existing unknown version `1` and missing/unreadable
installation behavior. Moving the read core does not change Skill content or
require a Skill version bump. Consumers need a kit release containing `skill`
before bumping their module pins; a local workspace alone is not a release.

## Defaults keep today's behavior

Moving a CLI onto the kit must not change what its users see. Behavior that
only some CLIs had is therefore opt-in:

| Option | Default | Who turns it on |
|---|---|---|
| `transport.Options.SameOriginRedirects` | off | wecom-calendar-cli. The Atlassian CLIs follow cross-origin redirects for attachments and media. |
| `transport.Options.RetryMethods` | GET and HEAD only | wecom-calendar-cli adds `PROPFIND` and `REPORT`. |
| `cliflags.InterpretEscapes` body flags | caller's set | bitbucket-cli passes its own body flags. |
| `errors.SetGuidance` | neutral hints | Every CLI installs its own. |
| `auth.Product.ListSchemes` | off | jenkins-cli, prometheus-cli and openobserve-cli name the accepted schemes in `AUTH_BAD_SCHEME`. |
| `auth.Product.AccountKeyIncludesPath` | off | prometheus-cli, which always keyed secrets by host and path. Turning it on elsewhere would orphan stored secrets. |
| `auth.Scheme` hooks (`Header`, `Validate`, `Decorate`) | the scheme's `Kind` decides | openobserve-cli for its pass-through token and browser session. |
| `config.URLRules.AssumeScheme` | a bare host is `BAD_BASE_URL` | jenkins-cli, prometheus-cli and openobserve-cli prefix `http://`. |
| `config.URLRules.TrimSuffix` | the path is kept | prometheus-cli trims a pasted API root. |
| `config.Paths.Legacy` | no legacy directory | confluence-cli and bitbucket-cli still read their pre-`~/.angelmsger` directory. |
| `config.Field.ResetBy` | nothing is reset | jira-cli, confluence-cli and bitbucket-cli drop a detected flavor when the server or flavor changes. |
| `config.Schema.AfterMerge` | none | jira-cli, confluence-cli and bitbucket-cli pick basic auth by default on Cloud. |
| `config.Schema.Validate` | URL, credential page and scheme only | jira-cli, confluence-cli and bitbucket-cli check the flavor; prometheus-cli checks the tenant. |
| `config.Schema.CheckPlan` | none | openobserve-cli refuses to repoint a browser-session context. |
| `config.Words.SchemeHint` | "Use one of: …" | wecom-calendar-cli explains its only scheme. |

The JSON a CLI prints (the error envelope, the list envelope, `_notice`, the
update status, the credential recovery errors, and the configuration errors,
`set-context` plan and auth guide) is pinned by tests. Changing it is a breaking change for
every CLI and every agent that reads them.

## Credential persistence failures

A successful keychain save removes the same account's older file fallback so
an outage cannot bring the old credential back. If that cleanup fails, `Save`
returns the keychain backend together with `*credstore.PartialSaveError`:
a non-empty backend together with an error signals partial persistence.
Callers can check those return values without depending on the new error type.
They must report that the credential was stored but cleanup is incomplete,
and request restored config-directory access before retrying the save.

After a failed keychain write, a file fallback is safe only when the account
is explicitly reported missing. If an older value exists or cannot be ruled
out, `Save` returns `*credstore.StoreAccessError` without writing a fallback.
Retry with access to the host user environment and an unlocked keychain. This
also means Linux sessions without a reachable D-Bus/Secret Service can now
fail to save instead of claiming a login whose credential may later be
shadowed. A probe that establishes there is no keychain, or an explicitly
missing account, still permits file storage. Existing fallback reads remain
available when the keychain cannot be read.

`Delete` attempts both backends and reports failures instead of claiming
logout succeeded. Missing accounts remain a successful, idempotent deletion;
other backend failures may leave credentials usable. Callers should preserve
that uncertainty and offer a host-access retry. Neither save nor delete errors
echo raw keychain diagnostics, which might contain credential material.

## Versioning

The kit is built for the family's own CLIs and stays below v1.0.0. A minor
release may change the Go API when that makes the kit simpler to use, and the
CLIs move to it together; it never changes the JSON a CLI prints. Each CLI pins
a released version in its `go.mod`. For work across the kit and a CLI at once,
use a `go.work` file at the workspace root rather than a `replace` directive.

## Development

```sh
gofmt -l .
go vet ./...
go test ./...
```

CI runs the tests on Linux, macOS and Windows. macOS is the only place the
keychain probe runs for real.

## License

MIT
