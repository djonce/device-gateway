#!/usr/bin/env bash
# Run the full test suite locally: Go + web typecheck/build + SDK.
# Mirrors the CI workflow in .github/workflows/ci.yml.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
cd "$ROOT"

echo "==> Go: vet + build + test"
go vet ./...
go build ./...
go test ./...

echo "==> Web: typecheck + build"
pnpm --dir web install --frozen-lockfile
pnpm --dir web run typecheck
pnpm --dir web run build

echo "==> SDK: typecheck + node:test"
( cd sdk && npm install --no-audit --no-fund && npm test )

echo "==> All checks passed."
