#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"

source_globs=(-g '*.go' -g '*.ts' -g '*.py' -g '!**/node_modules/**' -g '!**/.venv/**' -g '!**/__pycache__/**' -g '!**/vendor/**')

banned='^(model|models|type|types|util|utils|helper|helpers|common|misc|service|manager)\.(go|ts|py)$'
while IFS= read -r file; do
  base=${file##*/}
  if [[ "$base" =~ $banned ]]; then
    echo "vague source filename: $file" >&2
    exit 1
  fi
done < <(rg --files "${source_globs[@]}")

if rg -n '\belse\b' "${source_globs[@]}" .; then
  echo "line-of-sight violation: use a guard clause instead of else" >&2
  exit 1
fi

module=github.com/itsHabib/saas-reaper/specimens/notification-routing/internal
if rg -n -g '!**/*_test.go' "$module/(api|store|transport|worker)" internal/routing; then
  echo "policy boundary violation: internal/routing imports a transport or mechanism" >&2
  exit 1
fi

if rg -n "$module/(api|store|transport|worker)" internal/api internal/store internal/transport internal/worker; then
  echo "boundary violation: transports and mechanisms may depend only on routing policy" >&2
  exit 1
fi

echo "notification routing boundary checks passed"
