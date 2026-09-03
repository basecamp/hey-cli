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

Cross-package coverage, enforcing the floor set in the Makefile:

```sh
make coverage
```

It writes `coverage.out`, `coverage.func.txt` and `coverage.packages.txt`, then prints a
package summary and the lowest-covered functions.

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

This runs `fmt-check`, `vet`, `lint`, `test`, `tidy-check`, `check-surface`
and `check-release-lockstep`.

## CLI surface

`.surface` is the committed list of commands and flags. Adding or removing one
fails `make check-surface`; regenerate with `make update-surface` and commit the
diff. Removals also fail the release-time `make check-surface-compat` unless
listed in `.surface-breaking`.

Compares the current CLI surface against the previous tagged release to detect breaking changes.

## PR workflow

1. Fork and create a feature branch from `main`.
2. Make your changes.
3. Run `make check` and ensure everything passes.
4. Open a pull request against `main`.

## Releasing

See [RELEASING.md](RELEASING.md).
