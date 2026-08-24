.PHONY: build test test-unit test-e2e test-smoke preview-callback coverage fmt fmt-check vet lint tidy tidy-check \
	race-test vuln gosec secrets replace-check check-toolchain check security \
	release-check release test-release bench bench-save bench-compare \
	check-surface update-surface check-surface-compat check-size check-lint-lockstep \
	check-release-lockstep update-nix-hash tools clean install help

BINARY := $(CURDIR)/bin/hey
GOSEC_VERSION := v2.28.0
COVERAGE_FLOOR ?= 70.8
COVERAGE_PROFILE ?= coverage.out
COVERAGE_FUNCTIONS ?= coverage.func.txt
COVERAGE_PACKAGES ?= coverage.packages.txt
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
	@echo "  make test-e2e        Run the bats suite (installer and script contracts)"
	@echo "  make test-smoke      Run smoke tests against a live server"
	@echo "  make preview-callback Preview the OAuth callback screens in a browser"
	@echo "  make coverage        Run cross-package coverage and enforce the 70.8% floor"
	@echo "  make clean           Remove build artifacts"
	@echo "  make tidy            Tidy dependencies"
	@echo ""
	@echo "  make fmt             Format Go source files"
	@echo "  make fmt-check       Check formatting (CI gate)"
	@echo "  make vet             Run go vet"
	@echo "  make lint            Run golangci-lint"
	@echo "  make tidy-check      Verify go.mod/go.sum tidiness"
	@echo "  make check-lint-lockstep  Verify golangci-lint pins agree across workflows"
	@echo "  make race-test       Run unit tests with race detector"
	@echo "  make vuln            Run govulncheck"
	@echo "  make gosec           Run gosec static security analysis"
	@echo "  make secrets         Run gitleaks secret scan"
	@echo "  make replace-check   Guard against replace directives in go.mod"
	@echo ""
	@echo "  make check           fmt-check + vet + lint + test-unit + tidy-check"
	@echo "  make security        lint + vuln + gosec + secrets"
	@echo "  make release-check   check + replace-check + vuln + gosec + race-test"
	@echo "  make release         Run release preflight and tag (VERSION=v1.2.3 [DRY_RUN=1])"
	@echo "  make test-release    Dry-run the goreleaser pipeline (snapshot, no publish/sign)"
	@echo ""
	@echo "  make bench           Run benchmarks"
	@echo "  make bench-save      Save benchmark results"
	@echo "  make bench-compare   Compare saved benchmarks"
	@echo ""
	@echo "  make check-surface        Verify .surface matches the command tree"
	@echo "  make update-surface       Regenerate .surface"
	@echo "  make check-surface-compat Compare .surface against the previous release tag"
	@echo "  make check-size           Check the built binary against .size-budget"
	@echo "  make check-release-lockstep  Verify release tool pins and script references agree"
	@echo "  make update-nix-hash      Recompute the Nix vendorHash via Docker ([VERSION=v1.2.3] bumps the version)"
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

# Run repository-wide cross-package statement coverage and enforce the regression floor.
coverage: check-toolchain
	HEY_NO_KEYRING=1 GOWORK=off go test ./... -coverpkg=./... -covermode=atomic -coverprofile=$(COVERAGE_PROFILE)
	@./scripts/coverage-summary.sh $(COVERAGE_PROFILE) $(COVERAGE_FUNCTIONS) $(COVERAGE_PACKAGES)
	@./scripts/check-coverage.sh $(COVERAGE_PROFILE) $(COVERAGE_FLOOR)

# Serve the OAuth callback screens for visual review at http://127.0.0.1:9999.
# Pages re-render from disk on every request: edit internal/auth/callback*.html
# and refresh the browser. Ctrl-C to stop.
preview-callback: check-toolchain
	PREVIEW=1 go test -run TestPreviewCallbackPages ./internal/auth/ -count=1 -v -timeout=0

# Run the bats end-to-end suite (installers, release scripts). Needs bats-core.
test-e2e:
	@./tests/e2e/run.sh

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

# Run the same pinned gosec version as the security workflow.
gosec:
	GOWORK=off go run github.com/securego/gosec/v2/cmd/gosec@$(GOSEC_VERSION) ./...

# Run gitleaks secret scan. The scan is part of the security gate, so a missing
# binary or config fails it rather than passing it by skipping.
secrets:
	@if ! command -v gitleaks >/dev/null 2>&1; then \
		echo "ERROR: gitleaks not found; run make tools or see https://github.com/gitleaks/gitleaks"; \
		exit 1; \
	fi
	@if [ ! -f .gitleaks.toml ]; then \
		echo "ERROR: .gitleaks.toml absent"; \
		exit 1; \
	fi
	gitleaks detect --source . --verbose --redact

# Guard against replace directives in go.mod
replace-check:
	@if grep -q '^replace' go.mod; then \
		echo "ERROR: go.mod contains replace directives"; \
		grep '^replace' go.mod; \
		exit 1; \
	fi

# Local CI gate
check: fmt-check vet lint test-unit tidy-check check-surface check-release-lockstep

# Verify every workflow lints with the same golangci-lint version
check-lint-lockstep:
	@scripts/check-lint-lockstep.sh

# Verify the committed CLI surface snapshot (.surface) matches the command tree.
# TestSurfaceSnapshot is the authority: it diffs .surface against the cobra tree
# and fails on any drift, pointing here for regeneration.
check-surface: check-toolchain
	@HEY_NO_KEYRING=1 go test ./internal/cmd/ -run TestSurfaceSnapshot -count=1 || { \
		echo; echo "TestSurfaceSnapshot failed — if the committed .surface is stale, run: make update-surface"; \
		exit 1; \
	}

# Regenerate .surface from the current command tree (additions and removals
# alike; removals still have to be acknowledged in .surface-breaking for
# check-surface-compat), then verify the fresh snapshot passes.
update-surface: check-toolchain
	@HEY_NO_KEYRING=1 go test ./internal/cmd/ -run TestSurfaceSnapshot -count=1 -update-surface >/dev/null
	@HEY_NO_KEYRING=1 go test ./internal/cmd/ -run TestSurfaceSnapshot -count=1
	@echo ".surface updated"

# Compare .surface against the previous release tag (removals fail unless
# acknowledged in .surface-breaking)
check-surface-compat:
	@scripts/check-surface-compat.sh

# Check the binary just built against .size-budget. Passed explicitly so a
# stale dist/ from an earlier snapshot is never what gets measured.
check-size: build
	@scripts/check-size-budget.sh $(BINARY)

# Verify release tool pins and script references agree (wraps check-lint-lockstep)
check-release-lockstep:
	@scripts/check-release-lockstep.sh

# Recompute Nix vendorHash via Docker and update nix/package.nix. Pass
# VERSION=vX.Y.Z to also bump the package version; without it the stored
# version is kept. Releases stamp the version without Docker, then the tag
# workflow builds the exact flake before publication. 0 = updated, 2 = nothing
# to do. Anything else is a real failure and must propagate: a blanket
# `|| true` here would silently undo the script's own fail-closed check.
update-nix-hash:
	@V="$(VERSION)"; \
	if [ "$$V" = dev ]; then V=$$(sed -n 's/.*version = "\([^"]*\)".*/\1/p' nix/package.nix | head -1); fi; \
	scripts/update-nix-flake.sh "$${V#v}"; RC=$$?; \
	if [ $$RC -ne 0 ] && [ $$RC -ne 2 ]; then exit $$RC; fi

# Security suite
security: lint vuln gosec secrets

# Release preflight
release-check: check replace-check vuln gosec race-test check-surface-compat check-size

# Release (delegates to script)
release:
	@DRY_RUN=$(DRY_RUN) scripts/release.sh $(VERSION)

# Dry-run the goreleaser pipeline. Signing env is blanked so notarization and
# Authenticode are skipped rather than failing on missing secrets. SBOMs are
# skipped when syft is not on PATH (it is only installed in CI).
test-release:
	MACOS_SIGN_P12= MACOS_SIGN_PASSWORD= MACOS_NOTARY_KEY= MACOS_NOTARY_KEY_ID= MACOS_NOTARY_ISSUER_ID= \
	SM_API_KEY= SM_CLIENT_CERT_FILE= SM_CLIENT_CERT_PASSWORD= \
	HOMEBREW_TAP_TOKEN= \
	goreleaser release --snapshot --skip=publish,sign$$(command -v syft >/dev/null || echo ,sbom) --clean

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
# golangci-lint is pinned to the CI version (see scripts/check-lint-lockstep.sh)
# so local and CI findings agree. govulncheck stays @latest on purpose: pinning
# it only delays new advisories and Go version support.
tools:
	go install github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.11.1
	go install golang.org/x/vuln/cmd/govulncheck@latest
	@echo "For gitleaks, install via: brew install gitleaks (or see https://github.com/gitleaks/gitleaks)"
	@echo "For benchstat: go install golang.org/x/perf/cmd/benchstat@latest"

# Clean build artifacts
clean:
	rm -rf bin/
	rm -f $(COVERAGE_PROFILE) $(COVERAGE_FUNCTIONS) $(COVERAGE_PACKAGES)
	go clean

# Install binary to /usr/local/bin
install: build
	sudo install $(BINARY) /usr/local/bin/hey

# Tidy dependencies
tidy:
	go mod tidy
