# Changelog

All notable changes to `agent-cli-kit` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
