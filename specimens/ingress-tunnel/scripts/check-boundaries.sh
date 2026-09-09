#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"

# Vague filenames and line-of-sight are enforced repo-wide by the root scripts/check-boundaries.sh;
# this script owns only the specimen's own layering rules.
module=github.com/itsHabib/saas-reaper/specimens/ingress-tunnel/internal
if rg -n -g '!**/*_test.go' "$module/(api|edge|link|agent|store)" internal/tunnel; then
  echo "policy boundary violation: internal/tunnel imports a transport or mechanism" >&2
  exit 1
fi

if rg -n -g '!**/*_test.go' "$module/(api|edge|link|agent|store)" internal/api internal/edge internal/link internal/agent internal/store; then
  echo "boundary violation: transports and mechanisms may depend only on tunnel policy" >&2
  exit 1
fi

if rg -n -g '!**/*_test.go' 'coder/websocket|hashicorp/yamux' internal/tunnel internal/edge internal/agent internal/api internal/store; then
  echo "mechanism leak: only internal/link may speak WebSocket or yamux" >&2
  exit 1
fi

echo "ingress tunnel boundary checks passed"
