#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

banned='^(model|models|type|types|util|utils|helper|helpers|common|misc|service|manager)\.(go|ts|py)(\.tmpl)?$'
while IFS= read -r file; do
  base=${file##*/}
  if [[ "$base" =~ $banned ]]; then
    echo "vague source filename: $file" >&2
    exit 1
  fi
done < <(rg --files \
  -g '*.go' \
  -g '*.go.tmpl' \
  -g '*.ts' \
  -g '*.ts.tmpl' \
  -g '*.py' \
  -g '*.py.tmpl' \
  -g '!**/node_modules/**' \
  -g '!**/.venv/**' \
  -g '!**/__pycache__/**')

if rg -n '\belse\b' \
  -g '*.go' \
  -g '*.go.tmpl' \
  -g '*.ts' \
  -g '*.ts.tmpl' \
  -g '*.py' \
  -g '*.py.tmpl' \
  -g '!**/node_modules/**' \
  -g '!**/.venv/**' \
  -g '!**/__pycache__/**' \
  .; then
  echo "line-of-sight violation: use a guard clause instead of else" >&2
  exit 1
fi

if rg -n \
  -g '!**/*_test.go' \
  'saas-reaper/internal/(api|snapshot|store)' \
  internal/flags; then
  echo "policy boundary violation: internal/flags imports a mechanism or transport" >&2
  exit 1
fi

if rg -n 'saas-reaper/internal/api' internal/store internal/snapshot; then
  echo "mechanism boundary violation: persistence/projection imports transport" >&2
  exit 1
fi

echo "boundary checks passed"
