# Changelog

All notable changes to `agent-cli-kit` are documented in this file.

The format is based on [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and this project adheres to [Semantic Versioning](https://semver.org/spec/v2.0.0.html).

## [Unreleased]

### Added

- `errors`, `output`, `transport`, `credstore`, `cliflags` and `update`,
  reconciled from the copies in jira-cli, confluence-cli, bitbucket-cli,
  jenkins-cli, prometheus-cli, openobserve-cli and wecom-calendar-cli.
- `errors.SetGuidance` installs a CLI's own hints and next steps.
- `transport.Options.UserAgent`, `SameOriginRedirects` and `RetryMethods`.
  The redirect guard and extra retry methods are opt-in.
- `credstore.NewStore` takes the keychain service name, so each CLI keeps its
  existing entries.
- `update.Checker` takes the release repository and its endpoint override.
- Tests that pin the JSON of the error envelope, list envelope, pagination
  notice, input corrections and update status.
