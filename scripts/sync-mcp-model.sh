#!/bin/sh
# Sync the vendored hey-sdk model snapshot that MCP catalog generation reads.
#
# The catalog derives from hey-sdk's behavior model (per-operation traits:
# readonly, idempotent, pagination, retry) joined with its exported OpenAPI
# spec (operationId, method, path, tags, docs, parameter schemas). Both files
# are build products of hey-sdk's Smithy model, so we vendor a snapshot here
# rather than parse Smithy ourselves: CI stays hermetic and the reviewed diff
# shows exactly which surface changed when the SDK moves. Keep the snapshot in
# lockstep with the hey-sdk version pinned in go.mod.
#
# Usage: scripts/sync-mcp-model.sh [path-to-hey-sdk-checkout]
set -eu

sdk="${1:-../hey-sdk}"
dest="$(dirname "$0")/../internal/mcpserver/model"

# Resolve provenance before touching the destination, so a checkout that is
# not a git repo (or otherwise broken) can't leave a torn snapshot behind.
# --dirty marks a checkout with uncommitted changes, so modified model files
# are never recorded as a clean release tag.
commit=$(git -C "$sdk" rev-parse HEAD)
ref=$(git -C "$sdk" describe --tags --always --dirty)

for f in behavior-model.json openapi.json; do
  [ -f "$sdk/$f" ] || { echo "missing $sdk/$f (pass a hey-sdk checkout path)" >&2; exit 1; }
done

for f in behavior-model.json openapi.json; do
  cp "$sdk/$f" "$dest/$f"
done

cat > "$dest/PROVENANCE.json" <<JSON
{
  "source": "github.com/basecamp/hey-sdk",
  "commit": "$commit",
  "ref": "$ref",
  "files": ["behavior-model.json", "openapi.json"],
  "synced_by": "scripts/sync-mcp-model.sh"
}
JSON

echo "synced model from hey-sdk @ $ref ($commit)"
