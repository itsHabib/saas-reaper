#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

cd "$repo_dir"
go mod download

npm --prefix examples/typescript ci --silent

if [[ ! -x examples/python/.venv/bin/python ]]; then
  python3 -m venv examples/python/.venv
fi
examples/python/.venv/bin/python -m pip install \
  --disable-pip-version-check \
  --quiet \
  -r examples/python/requirements.txt

make -C specimens/webhook-delivery setup
make -C specimens/notification-routing setup
