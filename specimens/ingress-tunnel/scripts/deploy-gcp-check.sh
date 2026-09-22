#!/usr/bin/env bash
set -euo pipefail
specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"
work_dir=$(mktemp -d)
trap 'rm -rf -- "$work_dir"' EXIT
export TF_PLUGIN_CACHE_DIR="${TF_PLUGIN_CACHE_DIR:-${XDG_CACHE_HOME:-$HOME/.cache}/reaper-terraform-plugins}"
mkdir -p "$TF_PLUGIN_CACHE_DIR"
# Copy only tracked working-tree files: Terraform plans have no required extension.
# New source files must be staged before running this proof.
git ls-files -z -- . | rsync -a --from0 --files-from=- ./ "$work_dir/specimen/"
pack="$work_dir/specimen/deploy/gcp"
terraform -chdir="$pack" fmt -check -recursive
terraform -chdir="$pack" init -backend=false -input=false -no-color > /dev/null
terraform -chdir="$pack" validate -no-color
terraform -chdir="$pack" test -no-color
shellcheck deploy/gcp/startup.sh
bash -n deploy/gcp/startup.sh
GOTOOLCHAIN=local GOPROXY=off CGO_ENABLED=0 GOOS=linux GOARCH=amd64 \
  go build -trimpath -o "$work_dir/server" ./cmd/reaper-tunnel

# Render with Terraform itself, then check every embedded script and the real Caddy adapter.
terraform -chdir="$pack" console -no-color << 'EXPR' | jq -r . | base64 -d > "$work_dir/startup.sh"
base64encode(templatefile("startup.sh", {project="test-project",domain="tunnel.example.com",acme_email="ops@example.com",admin_actor="proof",bucket="test-artifacts",server_key="server-test",caddy_key="caddy-test",admin_secret="admin",read_secret="read",ipv6=false,origin_secret="",caddy_config=""}))
EXPR
python3 - "$work_dir/startup.sh" "$work_dir" << 'PYTHON'
import pathlib, re, sys
text = pathlib.Path(sys.argv[1]).read_text()
output = pathlib.Path(sys.argv[2])
for path, tag, body in re.findall(r"cat > ([^ ]+) << ?'([^']+)'\n(.*?)\n\2", text, re.S):
    if path.endswith('.sh') or path.endswith('/Caddyfile'):
        (output / pathlib.Path(path).name).write_text(body + '\n')
PYTHON
bash -n "$work_dir/startup.sh"
# cloud.sh supplies identifiers and functions to the embedded scripts.
shellcheck -e SC1091,SC2154,SC2050 "$work_dir/cloud.sh" "$work_dir/render-env.sh" "$work_dir/render-origin.sh"
caddy_version=$(terraform -chdir="$pack" console <<< 'local.caddy_version' | jq -r .)
dns_version=$(terraform -chdir="$pack" console <<< 'local.dns_version' | jq -r .)
xcaddy_version=$(terraform -chdir="$pack" console <<< 'local.xcaddy_version' | jq -r .)
GOBIN="$work_dir/tools" go install "github.com/caddyserver/xcaddy/cmd/xcaddy@$xcaddy_version"
"$work_dir/tools/xcaddy" build "$caddy_version" \
  --with "github.com/caddy-dns/googleclouddns@$dns_version" --output "$work_dir/caddy" > "$work_dir/caddy-build.log" 2>&1 || {
  cat "$work_dir/caddy-build.log" >&2
  exit 1
}
"$work_dir/caddy" adapt --config "$work_dir/Caddyfile" --adapter caddyfile > /dev/null
terraform -chdir="$pack" console -no-color << 'EXPR' | jq -r . | base64 -d > "$work_dir/Caddyfile-ipv6"
base64encode(templatefile("Caddyfile-ipv6", {acme_email="ops@example.com",control_host="control.example.com",edge_hosts="probe.example.com:443",control_cidrs="203.0.113.7/32",edge_cidrs="0.0.0.0/0 ::/0",cloudflare_cidrs="127.0.0.1/32"}))
EXPR
REAPER_ORIGIN_TOKEN=fixture-origin-secret "$work_dir/caddy" adapt --config "$work_dir/Caddyfile-ipv6" --adapter caddyfile > "$work_dir/ipv6.json"
python3 scripts/ipv6-proxy-proof.py "$work_dir/caddy" "$work_dir/ipv6.json" "$work_dir"

echo 'deploy check: GCP pack validates, mocked state transitions pass, rendered startup and pinned Caddy parse, server cross-compiles'
