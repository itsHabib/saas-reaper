#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
python_cache=$(mktemp -d)

cleanup() {
  if [[ "$python_cache" == /tmp/* || "$python_cache" == /var/folders/* ]]; then
    rm -rf -- "$python_cache"
  fi
}
trap cleanup EXIT

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
PYTHONPYCACHEPREFIX=$python_cache python3 -m py_compile verifier/verify.py
PYTHONPYCACHEPREFIX=$python_cache python3 -m unittest discover -s verifier -p 'verify_test.py'
go run mvdan.cc/sh/v3/cmd/shfmt@v3.13.1 -d -i 2 -ci -sr scripts
shellcheck scripts/*.sh
jq -e 'type == "array" and length > 0' fixtures/events.json > /dev/null
./scripts/check-boundaries.sh
