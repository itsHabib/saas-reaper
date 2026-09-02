#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"

banned='^(model|models|type|types|util|utils|helper|helpers|common|misc|service|manager)\.(go|ts|py)$'
while IFS= read -r file; do
  base=${file##*/}
  if [[ "$base" =~ $banned ]]; then
    echo "vague source filename: $file" >&2
    exit 1
  fi
done < <(rg --files \
  -g '*.go' \
  -g '*.ts' \
  -g '*.py' \
  -g '!**/node_modules/**' \
  -g '!**/.venv/**' \
  -g '!**/__pycache__/**')

if rg -n '\belse\b' \
  -g '*.go' \
  -g '*.ts' \
  -g '*.py' \
  -g '!**/node_modules/**' \
  -g '!**/.venv/**' \
  -g '!**/__pycache__/**' \
  .; then
  echo "line-of-sight violation: use a guard clause instead of else" >&2
  exit 1
fi

module=github.com/itsHabib/saas-reaper/specimens/webhook-delivery/internal
if rg -n -g '!**/*_test.go' "$module/(api|store|transport|worker)" internal/delivery; then
  echo "policy boundary violation: internal/delivery imports a transport or mechanism" >&2
  exit 1
fi

if rg -n \
  "$module/(api|store|transport|worker)" \
  internal/api internal/store internal/transport internal/worker; then
  echo "boundary violation: transports and mechanisms may depend only on delivery policy" >&2
  exit 1
fi

echo "webhook delivery boundary checks passed"
