#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)

cd "$specimen_dir"
go mod download
