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

## What each CLI supplies

The kit has no product names in it. A CLI passes them in once, at startup:

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

## Versioning

The kit follows semantic versioning, and its Go API is stable from v1.0.0: a
minor or patch release only adds, and a change that breaks a caller needs a new
major version with a `/v2` module path. The JSON a CLI prints is held to the
same rule. Each CLI pins a released version in its `go.mod`. For work across
the kit and a CLI at once, use a `go.work` file at the workspace root rather
than a `replace` directive.

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
