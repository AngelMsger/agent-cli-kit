# agent-cli-kit

Shared Go packages for the AngelMsger CLI family: jira-cli, confluence-cli,
bitbucket-cli, jenkins-cli, prometheus-cli, openobserve-cli and
wecom-calendar-cli. Each CLI used to carry its own copy of these packages, and
the copies drifted. The kit keeps one copy, so a fix such as the macOS keychain
probe lands once.

The kit holds code only. The family's principles and rules for building CLIs
that work well for both people and agents live in
[oa-cli](https://github.com/AngelMsger/oa-cli).

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

The JSON a CLI prints (the error envelope, the list envelope, `_notice`, and
the update status) is pinned by tests. Changing it is a breaking change for
every CLI and every agent that reads them.

## Versioning

The kit follows semantic versioning. Until v1.0.0, a minor release may change
the Go API but never the JSON a CLI prints. Each CLI pins a released version in
its `go.mod`. For work across the kit and a CLI at once, use a `go.work` file
at the workspace root rather than a `replace` directive.

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
