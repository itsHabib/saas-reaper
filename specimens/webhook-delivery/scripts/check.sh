#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
python_dir=$specimen_dir/receivers/python
python_cache=$(mktemp -d)

cleanup() {
  if [[ "$python_cache" == /tmp/* || "$python_cache" == /var/folders/* ]]; then
    rm -rf -- "$python_cache"
  fi
}
trap cleanup EXIT

cd "$specimen_dir"

if [[ ! -x receivers/typescript/node_modules/.bin/tsc ]]; then
  echo "TypeScript receiver dependencies are missing; run make setup" >&2
  exit 1
fi
if [[ ! -x "$python_dir/.venv/bin/python" ]]; then
  echo "Python receiver dependencies are missing; run make setup" >&2
  exit 1
fi

unformatted=$(gofmt -l cmd internal)
if [[ -n "$unformatted" ]]; then
  echo "gofmt required:" >&2
  echo "$unformatted" >&2
  exit 1
fi

go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run
go test -race ./...
npm --prefix receivers/typescript run check
PYTHONPYCACHEPREFIX=$python_cache \
  "$python_dir/.venv/bin/python" -m py_compile receivers/python/receiver.py
go run mvdan.cc/sh/v3/cmd/shfmt@v3.13.1 -d -i 2 -ci -sr scripts
shellcheck scripts/*.sh
jq -e 'type == "object"' fixtures/message.json > /dev/null
./scripts/check-boundaries.sh
