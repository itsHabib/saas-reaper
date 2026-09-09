#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

bash scripts/check-work.sh

unformatted=$(gofmt -l cmd examples/go internal)
if [[ -n "$unformatted" ]]; then
  echo "gofmt required:" >&2
  echo "$unformatted" >&2
  exit 1
fi

go vet ./...
go run github.com/golangci/golangci-lint/v2/cmd/golangci-lint@v2.12.2 run
go test -race ./...
go run mvdan.cc/sh/v3/cmd/shfmt@v3.13.1 -d -i 2 -ci -sr \
  scripts internal/factory/templates/common/scripts/check-work.sh.tmpl
shellcheck scripts/*.sh internal/factory/templates/common/scripts/check-work.sh.tmpl
go run github.com/rhysd/actionlint/cmd/actionlint@v1.7.12
npm --prefix examples/typescript run check
examples/python/.venv/bin/python -m py_compile examples/python/client.py
./scripts/check-boundaries.sh
./scripts/check-skill-projections.sh
./scripts/check-domain-adaptation.sh
make -C specimens/webhook-delivery check
make -C specimens/incident-escalation check
