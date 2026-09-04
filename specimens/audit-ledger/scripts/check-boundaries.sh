#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"

vendored=(
  -g '!**/node_modules/**'
  -g '!**/.venv/**'
  -g '!**/__pycache__/**'
)

banned='^(model|models|type|types|util|utils|helper|helpers|common|misc|service|manager)\.(go|ts|py)$'
while IFS= read -r file; do
  base=${file##*/}
  if [[ "$base" =~ $banned ]]; then
    echo "vague source filename: $file" >&2
    exit 1
  fi
done < <(rg --files -g '*.go' -g '*.ts' -g '*.py' "${vendored[@]}")

if rg -n '\belse\b' -g '*.go' -g '*.ts' -g '*.py' "${vendored[@]}" .; then
  echo "line-of-sight violation: use a guard clause instead of else" >&2
  exit 1
fi

module=github.com/itsHabib/saas-reaper/specimens/audit-ledger/internal
if rg -n -g '!**/*_test.go' "$module/(api|store)" internal/ledger; then
  echo "policy boundary violation: internal/ledger imports a transport or mechanism" >&2
  exit 1
fi

if rg -n "$module/api" internal/store; then
  echo "mechanism boundary violation: persistence imports transport" >&2
  exit 1
fi

if rg -n -g '!**/*_test.go' "$module/store" internal/api; then
  echo "transport boundary violation: HTTP depends on a concrete store outside tests" >&2
  exit 1
fi

echo "audit ledger boundary checks passed"
