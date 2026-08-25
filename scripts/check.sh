#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

unformatted=$(gofmt -l cmd examples/go internal)
if [[ -n "$unformatted" ]]; then
  echo "gofmt required:" >&2
  echo "$unformatted" >&2
  exit 1
fi

go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run
go test -race ./...
npm --prefix examples/typescript run check
examples/python/.venv/bin/python -m py_compile examples/python/client.py
./scripts/check-boundaries.sh
./scripts/check-skill-projections.sh
./scripts/check-domain-adaptation.sh
