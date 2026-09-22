#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"

unformatted=$(gofmt -l cmd internal)
if [[ -n "$unformatted" ]]; then
  echo "gofmt required:" >&2
  echo "$unformatted" >&2
  exit 1
fi

go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run
go test -race ./...
go run mvdan.cc/sh/v3/cmd/shfmt@v3.13.1 -d -i 2 -ci -sr scripts
shellcheck scripts/*.sh
jq -e 'type == "object"' fixtures/payload.json > /dev/null
./scripts/check-boundaries.sh
