#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"

globs=(
  -g '*.go'
  -g '*.ts'
  -g '*.py'
  -g '!**/node_modules/**'
  -g '!**/.venv/**'
  -g '!**/__pycache__/**'
  -g '!**/vendor/**'
)

banned='^(model|models|type|types|util|utils|helper|helpers|common|misc|service|manager)\.(go|ts|py)$'
while IFS= read -r file; do
  base=${file##*/}
  if [[ "$base" =~ $banned ]]; then
    echo "vague source filename: $file" >&2
    exit 1
  fi
done < <(rg --files "${globs[@]}")

if rg -n '\belse\b' "${globs[@]}" .; then
  echo "line-of-sight violation: use a guard clause instead of else" >&2
  exit 1
fi

module=github.com/itsHabib/saas-reaper/specimens/incident-escalation/internal

# Policy owns decisions; it may not reach for a transport, a store, or the worker.
# Test files are exempt from the two rules below because a package test composes
# real mechanisms exactly as the composition root does; production source is not.
if rg -n -g '!**/*_test.go' "$module/(api|store|transport|worker)" internal/incident internal/oncall; then
  echo "policy boundary violation: incident policy imports a transport or mechanism" >&2
  exit 1
fi

# On-call resolution is policy input; it may not depend on incident policy either.
if rg -n "$module/incident" internal/oncall; then
  echo "policy boundary violation: on-call resolution imports incident policy" >&2
  exit 1
fi

# Mechanisms depend on policy, never on each other.
if rg -n -g '!**/*_test.go' "$module/(store|transport|worker)" internal/api; then
  echo "boundary violation: the API imports a persistence or transport mechanism" >&2
  exit 1
fi

if rg -n "$module/(api|transport|worker)" internal/store; then
  echo "boundary violation: persistence imports a transport, the API, or the worker" >&2
  exit 1
fi

if rg -n "$module/(api|store|worker)" internal/transport; then
  echo "boundary violation: a transport imports persistence, the API, or the worker" >&2
  exit 1
fi

if rg -n "$module/(api|store|transport|incident|oncall)" internal/worker; then
  echo "boundary violation: the worker owns waiting only and consumes its own interface" >&2
  exit 1
fi

echo "incident escalation boundary checks passed"
