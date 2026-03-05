#!/usr/bin/env bash
# Collect CPU profile from benchmarks for Profile-Guided Optimization (PGO)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
PROFILE_DIR="${1:-$PROJECT_ROOT/profiles}"

cd "$PROJECT_ROOT"

echo "==> Creating profile directory: $PROFILE_DIR"
mkdir -p "$PROFILE_DIR"

echo "==> Running benchmarks with CPU profiling..."

PACKAGES=$(go list ./internal/...)
PROFILE_FILES=""
failed=0

for pkg in $PACKAGES; do
    pkg_name=$(basename "$pkg")
    profile_file="$PROFILE_DIR/bench_${pkg_name}.pprof"
    echo "    Profiling $pkg_name..."
    if ! go test -cpuprofile="$profile_file" \
        -bench=. \
        -benchtime=3s \
        -count=1 \
        "$pkg" 2>&1; then
        echo "    WARNING: $pkg_name benchmarks failed"
        failed=$((failed + 1))
    fi
    if [ -f "$profile_file" ] && [ -s "$profile_file" ]; then
        PROFILE_FILES="$PROFILE_FILES $profile_file"
    fi
done
rm -f ./*.test

if [ "$failed" -gt 0 ]; then
    echo "WARNING: $failed package(s) failed benchmarking — PGO profile may be incomplete"
fi

echo "==> Merging profiles..."
if [ -n "$PROFILE_FILES" ]; then
    go tool pprof -proto $PROFILE_FILES > "$PROFILE_DIR/merged.pprof"
else
    echo "Error: No profiles generated"
    exit 1
fi

echo "==> Converting to PGO format..."
cp "$PROFILE_DIR/merged.pprof" "$PROFILE_DIR/default.pgo"

# Copy to project root for -pgo=auto detection
cp "$PROFILE_DIR/default.pgo" "$PROJECT_ROOT/default.pgo"

echo "==> Profile statistics:"
go tool pprof -top -nodecount=10 "$PROFILE_DIR/merged.pprof" 2>/dev/null | head -20 || true

echo ""
echo "==> Profile saved to: $PROJECT_ROOT/default.pgo"
echo "    Size: $(du -h "$PROJECT_ROOT/default.pgo" | cut -f1)"
echo ""
echo "Build with PGO:"
echo "    go build -pgo=auto ./cmd/hey"
echo "    # or"
echo "    make build-pgo"
