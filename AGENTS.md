# Agent Guide

This file is the canonical instruction source for coding agents working in
`agent-cli-kit`.

## What this is

Shared Go packages used by every CLI in the AngelMsger family. A change here
reaches seven CLIs at once, so it is reviewed as a change to all of them. The
family's design principles and documentation live in
[agent-clis](https://github.com/AngelMsger/agent-clis); this repository holds code.

## Ground rules

- **No business product coupling.** No CLI names, business-product branches,
  binaries, environment variables or commands in package implementations. A CLI
  passes them through options, constructor arguments or hooks such as
  `errors.SetGuidance`. The shared Skill agent-directory IDs are integration
  metadata, not business-product branches; see the [package boundaries](README.md#skill-read-core).
- **Defaults keep current behavior.** Behavior that only some CLIs have is
  opt-in. A CLI that adopts a new kit version must not change what its users
  see unless it turns something on.
- **The wire format is a contract.** The `wire_test.go` files pin the JSON that
  CLIs print and agents parse. A change there is a breaking change for every
  CLI; make it only on purpose, note it in `CHANGELOG.md`, and update each
  CLI's docs.
- **Fix once, here.** When a bug in a shared package shows up in one CLI, fix
  it in the kit, then bump the kit in every CLI. Do not patch a CLI's copy.
- **Keep packages independent.** A package imports another kit package only
  when it must (for example `output` uses `errors`). No package imports a CLI.

## Test ownership

`output/wire_test.go` owns exact serialized bytes; `output/pagination_test.go`
owns the pagination behavior matrix, including destination, completion and
writer-failure ordering. Extend that matrix for another pagination case instead
of creating a second family suite. `output/family_test.go` retains independent
non-pagination contracts. CLI integration tests still own command wiring and
backend cursor replay. Keep distinct behavior assertions when consolidating tests.

## Regression boundaries

- Keep projected JSON keys unchanged, including literal dot paths; table
  rendering reads those projected keys. Preserve numeric JSON tokens without
  converting precise integers through `float64`.
- Redirect guards retain a bounded hop count, and policy rejections are not
  transient failures. Retry requests with bodies only when they are replayable.
- Credential save success must not hide an older credential in another backend.
  An inaccessible keychain is not evidence that its account is missing. Report
  partial saves and failed deletes honestly, preserve absent-account deletion
  idempotence, and never echo raw backend diagnostics containing secrets.

## Before you push

```sh
gofmt -l .
go vet ./...
GOOS=darwin go vet ./...
GOOS=windows go vet ./...
go test ./...
go mod tidy
```

`credstore` has macOS and Windows code paths; vet both before pushing, since
Linux tests do not compile them.

## Releasing

Tag `vX.Y.Z` on `main` after CI is green, and add the release to
`CHANGELOG.md`. Then bump the version in each CLI's `go.mod`. During local
cross-repo work, use a `go.work` file at the workspace root instead of a
`replace` directive, so nothing workspace-specific is committed to a CLI.
