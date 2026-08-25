#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
trial_dir=$(mktemp -d)

cleanup() {
  if [[ "$trial_dir" == /tmp/* || "$trial_dir" == /var/folders/* ]]; then
    rm -rf -- "$trial_dir"
  fi
}
trap cleanup EXIT

mkdir -p "$trial_dir/repo"
rsync -a \
  --exclude .git \
  --exclude .reaper \
  --exclude node_modules \
  --exclude .venv \
  --exclude __pycache__ \
  "$repo_dir/" \
  "$trial_dir/repo/"

cd "$trial_dir/repo"
git init --quiet
git apply fixtures/add-organization-plan.patch
gofmt -w internal/flags/context.go internal/flags/evaluate_test.go
go test -race ./internal/flags
GOLANGCI_LINT_CACHE="$trial_dir/golangci-cache" \
  go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run ./internal/flags/...
./scripts/check-boundaries.sh

rg --quiet 'organization\.plan' internal/flags/context.go
rg --quiet 'organization\.plan' internal/flags/evaluate_test.go
rg --quiet 'organization\.plan' DOMAIN.md
rg --quiet 'organization\.plan' REAPER.yaml
rg --quiet 'organization\.plan' README.md

echo "bounded domain-adaptation control passed"
