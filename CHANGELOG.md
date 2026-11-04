# Changelog

All notable changes to `agent-cli-kit` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

## [0.7.0] - 2026-11-04

### Changed

- `transport`: the default Doer no longer follows a redirect of a write. A
  GET, a HEAD or a method in `RetryMethods` still follows redirects as
  before (same origin only with `SameOriginRedirects`); any other method gets
  the 3xx response itself. Go followed a 301, 302 or 303 after a write with a
  GET, so a login page's 200 could read as the write's acknowledgment, and it
  followed a 307 or 308 by sending the write again to the new location. A CLI
  that checks for a 2xx now sees the redirect's status, and `FromWrite`
  reports it as an unknown outcome. A CLI whose server acknowledges a write
  with a redirect (Jenkins answers stop and cancel with a 302 to the previous
  page) must accept that redirect itself.
- `transport`: GET, HEAD and `RetryMethods` are matched the same way for
  retries and redirects: case-insensitively, and an empty method (which
  `net/http` sends as a GET) counts as a GET. A hand-built request with an
  empty method used to be neither retried nor, under the new redirect rule,
  followed.
- `errors.ReadOnlyBlocked`: a `ReadOnlyMode` with only `Env` names only that
  variable in its hint. It used to add "or defaults.read_only", a setting a
  CLI without a config file (wecom-channel-cli) does not have.
- `errors.FromWrite`: an unknown outcome caused by a 3xx response has its own
  hint. It still says not to repeat the write, and names what a redirect
  usually means (a login page, a single sign-on proxy, a base URL that should
  use https) before the read command.
- `errors.FromWrite`: an error that already reports a write outcome (from
  `FromWrite`, `WriteSucceeded` or `MissingWriteIdentity`) is returned
  unchanged. A layer that classifies a write its inner client already
  classified, such as a decorator around a client, used to nest one outcome
  inside another ("Write outcome is unknown for …: Write outcome is unknown
  for …").

## [0.6.0] - 2026-11-02

### Added

- `errors`: the family's write-outcome rule, moved from jira-cli so Bitbucket,
  Confluence and Jenkins apply the same one. `FromWrite(err, WriteTarget)`
  returns a definite refusal (usage, config, auth, permission, not found,
  conflict) unchanged. Every other failure, including one that is not a
  `CLIError` such as a raw context or `*url.Error`, becomes "Write outcome is
  unknown": not retryable, hint "Do not repeat the write", the target's read
  commands as next steps, and the cause's category, code, HTTP status and
  details kept.
  The exception is a 2xx response that could not be decoded: that write was
  applied and reports `WRITE_SUCCEEDED_RESPONSE_INVALID`. `WriteSucceeded`
  and `MissingWriteIdentity` report other applied writes whose result cannot
  be shown (`WRITE_SUCCEEDED_RESPONSE_INVALID`, `WRITE_SUCCEEDED_READ_FAILED`).
  `ShellArg` quotes a value for a suggested POSIX shell command. jira-cli
  prints the same errors on this rule as before, except that an internal error
  during a write (such as failing to encode the request) is now an unknown
  outcome too.
- `errors.ReadOnlyBlocked`: one `READONLY_BLOCKED` wording for the family.
  The next steps offer `--dry-run` (when the CLI has it) and `--allow-writes`
  only for a write the user authorized; they never turn read-only mode off,
  which is the user's setting. The hint names the CLI's switches from
  `ReadOnlyMode` (environment variable, config file) as the user's to change.
  The code, category and exit code are unchanged; the message, hint and next
  steps change in each CLI that adopts it.
- `terminal`: `Is` asks the operating system whether a stream is a terminal,
  so `/dev/null` and pipes are not; `Prompter` asks for a line, a choice, a
  yes/no confirmation or a hidden secret on the command's own streams, and
  reads nothing when its question cannot be written; `ReadLine` never reads
  past the newline, and a read failure other than the end of input returns
  its error rather than a truncated line. Replaces the copies in each CLI.

### Fixed

- `transport`: the `Verbose` request log masks a password in the request URL
  (`https://user:xxxxx@host/...`), as `net/http` error messages already do, so
  a base URL with a password in it no longer prints the password under
  `--verbose`. The username, path and query string are still written as sent,
  and an error as the Doer returned it. The log line is otherwise unchanged.

## [0.5.0] - 2026-10-24

### Added

- `reuse`: the `auth reuse` decision every CLI carried in its own
  `internal/app/auth_reuse.go`, rewritten for the family standard's "Reuse an
  existing login". A CLI describes its stored contexts (`Context`: service
  identity, credential-key spelling, scheme, username) and supplies
  resolution, verification (optionally returning the server-side `Account`)
  and persistence; `Run` decides. Sources are considered in three steps
  (contexts under the destination's scheme, long-lived credentials under any
  scheme, browser sessions) and include secrets stored for the destination's
  service that no context points at, paired with the usernames recorded on
  the service. A username is an identity only under a scheme that sends it.
  The first step keeps the earlier behavior except for two pre-access
  exclusions and a stand-in lookup; every other check runs after it, within
  one deadline (`DefaultWindow`, narrowed by the request timeout), adopts only
  on reliable evidence, and otherwise returns the earlier result, errors
  included. `--from-scheme` selects a stored secret, and an explicitly
  selected long-lived source may replace a browser session. The result adds
  `scheme`, `username`, `account`, `account_name` and `next_steps`;
  `AUTH_REUSE_AMBIGUOUS` adds `details.candidates`. `MayReuse` is the offline
  check that decides whether a sign-in step should list `auth reuse` first.
- `credstore`: `Store.Lookup` reports what the keychain and the file each
  answered (`Hit`, `Miss`, `Failed`, `Skipped`). `Load` is built on it and
  returns what it returned before.
- `auth`: `Scheme.Session` marks a captured browser session; `UsesUsername`,
  `IsSession`, `NeedsCredential` and `SchemeNames` describe the registered
  schemes; `ResolveLookup` is `Resolve` that also returns the store's answer;
  `MissingError` and `InaccessibleError` expose the two store errors.
- `config`: `RegisteredWords` returns the command wording a CLI registered.

## [0.4.0] - 2026-10-14

### Added

- `skill`: shared read-only version/load-state and installation inspection, plus agent integration directory metadata; callers retain their environment, output and filesystem mutation policies.

### Changed

- Consolidate output pagination behavior in one case matrix and exact serialized notices in wire tests, retaining independent non-pagination contracts and adding later-row write-failure coverage. Production output behavior is unchanged.

### Fixed

- `config`: deleting the current context now matches its saved reference
  case-insensitively, consistent with lookup, so a differently cased reference
  cannot leave subsequent configuration loads pointing at a deleted context.
- `output`: render nested field projections in tables without losing their
  values; retain flattened JSON keys and preserve large integers and precise
  numeric JSON tokens in every output format.
- `transport`: bound same-origin redirects to ten hops and stop retrying
  redirect-policy failures. Do not retry consumed bodies without `GetBody`.
- `credstore`: report keychain deletion failures while still attempting file
  cleanup; missing accounts remain an idempotent success.
- `credstore`: after a failed keychain write, only save a file fallback when the
  keychain account is confirmed missing. An existing or unreadable account now
  fails explicitly, including unreachable Linux Secret Service sessions where
  absence cannot be established. This prevents older credentials from masking
  a reportedly successful login after keychain recovery.
- `credstore`: remove an account's stale file fallback after a successful
  keychain save. Return its backend and `PartialSaveError` if cleanup fails so
  callers can report partial persistence without exposing credential data.

## [0.3.0] - 2026-10-11

### Added

- `config`: the configuration engine every CLI carried in its own
  `internal/config`. A CLI registers a field table (`Field`: environment
  names, default, team preset, the scheme a secret implies), its file
  location (`Paths`) and its command wording (`Words`) with `Register`, keeps
  its own structs and YAML shape, and moves values with `Encode` and
  `Decode`. The package provides `Load` with per-field provenance and context
  selection, `StoredContext`, `DefaultValues`, `File` (`Context`,
  `ContextNames`, `Upsert`, `Remove`), `ReadFile`/`WriteFile` with an atomic
  0600 write, `ResolveDir`, `NormalizeServiceURL`, `ValidateService`,
  `PlanServiceContext`, `AuthGuide`,
  `WithCredentialGuide`, `UseContext`, `DeleteContext`, the
  `UNKNOWN_CONTEXT` hint and error, and the `--help` reminder block and
  `_notice` payload for an implicitly chosen context. Reconciled from the
  seven CLIs. An assumed `http://`, a trimmed API root, a legacy directory,
  reset-by fields, and the `AfterMerge`, `Validate` and `CheckPlan` hooks are
  opt-in.
- `UNKNOWN_CONTEXT` always names the listing command in `next_steps`. A CLI
  that did not print it before gains the field when it adopts the package.
- Tests that pin the JSON of the configuration errors, the `set-context`
  plan, the auth guide and the context notice, and golden files for the YAML
  the package writes.

## [0.2.0] - 2026-10-11

### Added

- `auth`: scheme table (`Register`, `Scheme`, `Kind`), `Credential` with
  `Validate`, `Header`, `Decorator` and `Redacted`, `AccountKey`, `Resolve`,
  `Save`, `Forget`, and the shared recovery errors
  `CREDENTIAL_NOT_VISIBLE_OR_MISSING`, `CREDENTIAL_STORE_INACCESSIBLE` and
  `CREDENTIAL_SERVICE_MISMATCH`. Reconciled from the seven CLIs'
  `internal/auth`. Listing schemes in `AUTH_BAD_SCHEME`, keying secrets by URL
  path, and the openobserve-style scheme hooks are opt-in.
- A test that pins the JSON of both credential recovery errors.

## [0.1.0] - 2026-10-11

### Added

- `errors`, `output`, `transport`, `credstore`, `cliflags` and `update`,
  reconciled from the copies in jira-cli, confluence-cli, bitbucket-cli,
  jenkins-cli, prometheus-cli, openobserve-cli and wecom-calendar-cli.
- `errors.SetGuidance` installs a CLI's own hints and next steps.
- `transport.Options.UserAgent`, `SameOriginRedirects` and `RetryMethods`.
  The redirect guard and extra retry methods are opt-in.
- `credstore.NewStore` takes the keychain service name, so each CLI keeps its
  existing entries. Only `NewStore` runs the macOS keychain probe;
  `NewStoreWithBackend` always uses the backend it is given, so tests behave
  the same on every OS.
- `update.Checker` takes the release repository and its endpoint override.
- Tests that pin the JSON of the error envelope, list envelope, pagination
  notice, input corrections and update status.
