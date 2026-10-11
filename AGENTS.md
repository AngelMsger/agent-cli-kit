# Agent Guide

This file is the canonical instruction source for coding agents working in
`agent-cli-kit`. [`CLAUDE.md`](CLAUDE.md) only points here.

## What this is

Shared Go packages used by every CLI in the AngelMsger family. A change here
reaches seven CLIs at once, so it is reviewed as a change to all of them. The
family's design principles and documentation live in
[oa-cli](https://github.com/AngelMsger/oa-cli); this repository holds code.

## Ground rules

- **No product names.** Nothing here may name a CLI, product, binary,
  environment variable or command. A CLI passes those in through options,
  constructor arguments or hooks such as `errors.SetGuidance`.
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
