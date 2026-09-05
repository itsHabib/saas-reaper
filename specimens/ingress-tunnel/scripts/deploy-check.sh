#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"

if ! command -v terraform > /dev/null; then
  echo "terraform is required for the deployment pack check" >&2
  exit 1
fi

work_dir=$(mktemp -d)
cleanup() {
  if [[ "$work_dir" == /tmp/* || "$work_dir" == /var/folders/* ]]; then
    rm -rf -- "$work_dir"
  fi
}
trap cleanup EXIT

export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-$work_dir/plugin-cache}"
mkdir -p "$TF_PLUGIN_CACHE_DIR"

terraform -chdir=deploy/aws fmt -check
terraform -chdir=deploy/aws init -backend=false -input=false -no-color > /dev/null
terraform -chdir=deploy/aws validate -no-color > /dev/null
shellcheck deploy/aws/user-data.sh

# The pack cross-compiles the server for the instance; prove the build the apply will run.
GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0 GOOS=linux GOARCH=arm64 \
  go build -trimpath -o "$work_dir/reaper-tunnel-linux-arm64" ./cmd/reaper-tunnel

echo "deploy check: the AWS pack is formatted, valid, and its server binary cross-compiles"
