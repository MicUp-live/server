#!/usr/bin/env bash
set -euo pipefail

# check-fitness.sh — Architecture fitness checks for MicUp.live
# Enforces Stage A maintainability guardrails:
# 1. Canonical OpenAPI equivalence
# 2. Frontend design token validation
# 3. Go code formatting (gofmt)
# 4. Go code vetting (go vet)
# 5. Enforced hotspot baseline & exception policy (scripts/hotspot-baseline.json)

REPO_ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "${REPO_ROOT}"

echo "=== 1. Checking Canonical OpenAPI Sync ==="
python3 scripts/sync-openapi.py --check

echo "=== 2. Checking Frontend Design Tokens ==="
npm --prefix web run check:tokens

echo "=== 3. Checking Go Code Formatting ==="
UNFORMATTED=$(gofmt -l internal/ cmd/ main.go 2>/dev/null || true)
if [ -n "${UNFORMATTED}" ]; then
  echo "Error: The following Go files are not formatted with gofmt:"
  echo "${UNFORMATTED}"
  echo "Run 'go fmt ./...' to fix."
  exit 1
fi
echo "Go formatting clean."

echo "=== 4. Checking Go Vet ==="
go vet ./...
echo "Go vet clean."

echo "=== 5. Checking Maintainability Hotspot Baseline & Policy ==="
python3 scripts/check-hotspots.py
python3 scripts/check-hotspots.py --self-test

echo ""
echo "All architecture fitness checks passed."
