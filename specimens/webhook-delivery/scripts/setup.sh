#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
python_dir=$specimen_dir/receivers/python

cd "$specimen_dir"
go mod download
npm --prefix receivers/typescript ci --silent

if [[ ! -x "$python_dir/.venv/bin/python" ]]; then
  python3 -m venv "$python_dir/.venv"
fi
"$python_dir/.venv/bin/python" -m pip install \
  --disable-pip-version-check \
  --quiet \
  --requirement "$python_dir/requirements.txt"
