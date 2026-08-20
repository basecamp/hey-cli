# Contributing to hey-cli

## Prerequisites

- [Go](https://go.dev/dl/) (version in `go.mod`)
- [golangci-lint](https://golangci-lint.run/) v2+ (`make tools` installs the pinned version)
- [bats-core](https://github.com/bats-core/bats-core) and `jq` for `make test-e2e`
- [mise](https://mise.jdx.dev/) (optional, for toolchain management)

Install dev tools:

```sh
make tools
```

## Build

```sh
make build
```

The binary is written to `bin/hey`.

## Test

```sh
make test
```

Run with race detector:

```sh
make race-test
```

The bats suite covers the installers and release scripts (`scripts/install.sh`,
`scripts/install.ps1`, `scripts/notify-issue.sh`, …), which Go tests cannot reach:

```sh
make test-e2e
```

Tests that exercise `install.ps1` need `pwsh` and skip without it locally.

## Lint

```sh
make lint
```

Check formatting:

```sh
make fmt-check
```

## Full local CI gate

```sh
make check
```

This runs `fmt-check`, `vet`, `lint`, `test`, and `tidy-check`.

## CLI surface compatibility

```sh
make check-surface-compat
```

Compares the current CLI surface against the previous tagged release to detect breaking changes.

## PR workflow

1. Fork and create a feature branch from `main`.
2. Make your changes.
3. Run `make check` and ensure everything passes.
4. Open a pull request against `main`.
