.PHONY: build test test-unit test-smoke fmt fmt-check vet lint tidy tidy-check \
	race-test vuln secrets replace-check check-toolchain check security \
	release-check release bench bench-save bench-compare \
	check-surface check-surface-compat tools clean install help

BINARY := $(CURDIR)/bin/hey
# Local builds are "dev": a git-describe SHA would make them look like releases.
VERSION ?= dev
LDFLAGS := -s -w \
	-X github.com/basecamp/hey-cli/internal/version.Version=$(VERSION) \
	-X github.com/basecamp/hey-cli/internal/version.Commit=$(shell git rev-parse --short HEAD 2>/dev/null || echo none) \
	-X github.com/basecamp/hey-cli/internal/version.Date=$(shell date -u +%Y-%m-%dT%H:%M:%SZ)

help:
	@echo "HEY CLI"
	@echo ""
	@echo "Usage:"
	@echo "  make build           Build the CLI"
	@echo "  make test-unit       Run unit tests"
	@echo "  make test            Alias for test-unit"
	@echo "  make test-smoke      Run smoke tests against a live server"
	@echo "  make clean           Remove build artifacts"
	@echo "  make tidy            Tidy dependencies"
	@echo ""
	@echo "  make fmt             Format Go source files"
	@echo "  make fmt-check       Check formatting (CI gate)"
	@echo "  make vet             Run go vet"
	@echo "  make lint            Run golangci-lint"
	@echo "  make tidy-check      Verify go.mod/go.sum tidiness"
	@echo "  make race-test       Run unit tests with race detector"
	@echo "  make vuln            Run govulncheck"
	@echo "  make secrets         Run gitleaks secret scan"
	@echo "  make replace-check   Guard against replace directives in go.mod"
	@echo ""
	@echo "  make check           fmt-check + vet + lint + test-unit + tidy-check"
	@echo "  make security        lint + vuln + secrets"
	@echo "  make release-check   check + replace-check + vuln + race-test"
	@echo "  make release         Run release preflight and tag (VERSION=v1.2.3 [DRY_RUN=1])"
	@echo ""
	@echo "  make bench           Run benchmarks"
	@echo "  make bench-save      Save benchmark results"
	@echo "  make bench-compare   Compare saved benchmarks"
	@echo ""
	@echo "  make check-surface        Generate CLI surface snapshot"
	@echo "  make check-surface-compat Compare surface against previous tag"
	@echo "  make tools                Install dev tools"

# Toolchain guard — fails fast when PATH go and GOROOT go disagree
check-toolchain:
	@GOV=$$(go version | awk '{print $$3}'); \
	ROOT=$$(go env GOROOT); \
	ROOTV=$$($$ROOT/bin/go version | awk '{print $$3}'); \
	if [ "$$GOV" != "$$ROOTV" ]; then \
		echo "ERROR: Go toolchain mismatch"; \
		echo "  PATH go:   $$GOV ($$(which go))"; \
		echo "  GOROOT go: $$ROOTV ($$ROOT/bin/go)"; \
		echo "Fix: eval \"\$$(mise hook-env)\" && make <target>"; \
		exit 1; \
	fi

# Build CLI
build: check-toolchain
	@mkdir -p bin
	go build -ldflags "$(LDFLAGS)" -o $(BINARY) ./cmd/hey

# Run unit tests
test-unit: check-toolchain
	go test -v ./internal/...

# Alias for test-unit
test: test-unit

# Run smoke tests against a live HEY server.
# Requires: a running server (default http://app.hey.localhost:3003) and Chrome.
# Override defaults: make test-smoke HEY_SMOKE_BASE_URL=... HEY_SMOKE_EMAIL=... HEY_SMOKE_PASSWORD=...
test-smoke: build
	cd tests/smoke && go test -v -count=1 -timeout 5m ./...

# Format Go source
fmt:
	gofmt -s -w .

# Check formatting (CI gate)
fmt-check:
	@test -z "$$(gofmt -l .)" || (echo "Files not formatted:"; gofmt -l .; exit 1)

# Run go vet
vet: check-toolchain
	go vet ./...

# Run golangci-lint
lint:
	golangci-lint run ./...

# Verify go.mod/go.sum tidiness (non-mutating)
tidy-check:
	@set -eu; \
	trap 'mv -f go.mod.bak go.mod; mv -f go.sum.bak go.sum' EXIT; \
	cp go.mod go.mod.bak; \
	cp go.sum go.sum.bak; \
	go mod tidy; \
	if ! diff -q go.mod go.mod.bak >/dev/null 2>&1 || ! diff -q go.sum go.sum.bak >/dev/null 2>&1; then \
		echo "go.mod or go.sum is not tidy — run 'go mod tidy'"; \
		exit 1; \
	fi

# Run unit tests with race detector
race-test: check-toolchain
	go test -race -count=1 ./internal/...

# Run govulncheck
vuln:
	govulncheck ./...

# Run gitleaks secret scan
secrets:
	@if ! command -v gitleaks >/dev/null 2>&1 || [ ! -f .gitleaks.toml ]; then \
		echo "Skipping gitleaks (binary not found or .gitleaks.toml absent)"; \
	else \
		gitleaks detect --source . --verbose; \
	fi

# Guard against replace directives in go.mod
replace-check:
	@if grep -q '^replace' go.mod; then \
		echo "ERROR: go.mod contains replace directives"; \
		grep '^replace' go.mod; \
		exit 1; \
	fi

# Local CI gate
check: fmt-check vet lint test-unit tidy-check

# Generate CLI surface snapshot
check-surface: build
	@scripts/check-cli-surface.sh $(BINARY)

# Compare surface against previous tag
check-surface-compat: build
	@PREV_TAG=$$(git describe --tags --abbrev=0 HEAD^ 2>/dev/null || echo ""); \
	if [ -z "$$PREV_TAG" ]; then \
		echo "No previous tag — skipping surface compatibility check"; \
	else \
		scripts/check-cli-surface.sh $(BINARY) /tmp/current-surface.txt; \
		SCRIPT_DIR=$$(pwd)/scripts; \
		WORKTREE_DIR=/tmp/baseline-tree-$$$$; \
		trap 'git worktree remove "$$WORKTREE_DIR" --force 2>/dev/null || true' EXIT; \
		git worktree add "$$WORKTREE_DIR" "$$PREV_TAG"; \
		if ! (cd "$$WORKTREE_DIR" && make build); then \
			echo "ERROR: baseline ($$PREV_TAG) failed to build"; \
			exit 1; \
		fi; \
		if "$$SCRIPT_DIR/check-cli-surface.sh" "$$WORKTREE_DIR/bin/hey" /tmp/baseline-surface.txt 2>/dev/null; then \
			scripts/check-cli-surface-diff.sh /tmp/baseline-surface.txt /tmp/current-surface.txt; \
		else \
			echo "Baseline ($$PREV_TAG) does not support --help --agent — skipping surface diff"; \
		fi; \
	fi

# Security suite
security: lint vuln secrets

# Release preflight
release-check: check replace-check vuln race-test

# Release (delegates to script)
release:
	@DRY_RUN=$(DRY_RUN) scripts/release.sh $(VERSION)

# Run benchmarks
bench: check-toolchain
	go test -bench=. -benchmem ./internal/...

# Save benchmark results for comparison
bench-save: check-toolchain
	@mkdir -p profiles
	go test -bench=. -benchmem -count=5 ./internal/... > profiles/benchmarks-$$(date +%Y%m%d-%H%M%S).txt
	@echo "Saved to profiles/benchmarks-$$(date +%Y%m%d-%H%M%S).txt"

# Compare two most recent benchmark saves
bench-compare:
	@LATEST=$$(ls -t profiles/benchmarks-*.txt 2>/dev/null | head -1); \
	PREV=$$(ls -t profiles/benchmarks-*.txt 2>/dev/null | head -2 | tail -1); \
	if [ -z "$$LATEST" ] || [ -z "$$PREV" ] || [ "$$LATEST" = "$$PREV" ]; then \
		echo "Need at least two benchmark saves — run 'make bench-save' twice"; \
		exit 1; \
	fi; \
	echo "Comparing $$PREV → $$LATEST"; \
	benchstat "$$PREV" "$$LATEST"

# Install dev tools
tools:
	go install github.com/golangci/golangci-lint/cmd/golangci-lint@latest
	go install golang.org/x/vuln/cmd/govulncheck@latest
	@echo "For gitleaks, install via: brew install gitleaks (or see https://github.com/gitleaks/gitleaks)"
	@echo "For benchstat: go install golang.org/x/perf/cmd/benchstat@latest"

# Clean build artifacts
clean:
	rm -rf bin/
	go clean

# Install binary to /usr/local/bin
install: build
	sudo install $(BINARY) /usr/local/bin/hey

# Tidy dependencies
tidy:
	go mod tidy
