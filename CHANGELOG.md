# Changelog

All notable changes to `agent-cli-kit` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

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
