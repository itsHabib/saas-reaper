#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"
bash internal/factory/templates/common/scripts/check-work.sh.tmpl "$@"
