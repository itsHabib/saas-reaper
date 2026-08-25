#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$repo_dir"

work_dir=$(mktemp -d)
server_pid=""
cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  rm -rf "$work_dir"
}
trap cleanup EXIT

go build -o "$work_dir/reaper" ./cmd/reaper
"$work_dir/reaper" catalog | jq -e '
  [.languages[].value] == ["go", "typescript", "python"] and
  [.databases[].value] == ["sqlite", "postgres"] and
  ([.deployments[].value] | index("aws-ecs")) != null and
  ([.deployments[].value] | index("gcp-cloud-run")) != null and
  ([.deployments[].value] | index("kubernetes")) != null
' >/dev/null

"$work_dir/reaper" generate \
  --recipe recipes/go-sqlite-docker.yaml \
  --out "$work_dir/go-local" >/dev/null
REAPER_ADMIN_TOKEN=demo-admin \
REAPER_EVALUATION_TOKEN=demo-evaluation \
  docker compose -f "$work_dir/go-local/deploy/docker/compose.yaml" config --quiet
make -C "$work_dir/go-local" setup check demo

"$work_dir/reaper" generate \
  --recipe recipes/typescript-sqlite-docker.yaml \
  --out "$work_dir/typescript-one" >/dev/null
"$work_dir/reaper" generate \
  --recipe recipes/typescript-sqlite-docker.yaml \
  --out "$work_dir/typescript-two" >/dev/null
diff -ru "$work_dir/typescript-one" "$work_dir/typescript-two" >/dev/null
unzip -t "$work_dir/typescript-one.zip" >/dev/null
REAPER_ADMIN_TOKEN=demo-admin \
REAPER_EVALUATION_TOKEN=demo-evaluation \
  docker compose -f "$work_dir/typescript-one/deploy/docker/compose.yaml" config --quiet
make -C "$work_dir/typescript-one" setup check demo

"$work_dir/reaper" generate \
  --recipe recipes/python-sqlite-docker.yaml \
  --out "$work_dir/python-local" >/dev/null
make -C "$work_dir/python-local" setup check demo

"$work_dir/reaper" generate \
  --recipe recipes/typescript-postgres-docker.yaml \
  --out "$work_dir/postgres-local" >/dev/null
REAPER_ADMIN_TOKEN=demo-admin \
REAPER_EVALUATION_TOKEN=demo-evaluation \
  docker compose -f "$work_dir/postgres-local/deploy/docker/compose.yaml" config --quiet

"$work_dir/reaper" generate \
  --recipe recipes/go-postgres-aws.yaml \
  --out "$work_dir/aws-ecs" >/dev/null
make -C "$work_dir/aws-ecs" setup check
terraform -chdir="$work_dir/aws-ecs/deploy/aws-ecs" fmt -check

"$work_dir/reaper" generate \
  --recipe recipes/python-postgres-gcp.yaml \
  --out "$work_dir/gcp-cloud-run" >/dev/null
make -C "$work_dir/gcp-cloud-run" setup check
terraform -chdir="$work_dir/gcp-cloud-run/deploy/gcp-cloud-run" fmt -check

"$work_dir/reaper" generate \
  --recipe recipes/python-postgres-kubernetes.yaml \
  --out "$work_dir/kubernetes" >/dev/null
unzip -t "$work_dir/kubernetes.zip" >/dev/null
test -f "$work_dir/kubernetes/deploy/kubernetes/base/deployment.yaml"
test -f "$work_dir/kubernetes/deploy/kubernetes/base/availability.yaml"

"$work_dir/reaper" generate \
  --recipe recipes/typescript-sqlite-ec2.yaml \
  --out "$work_dir/aws-ec2" >/dev/null
terraform -chdir="$work_dir/aws-ec2/deploy/aws-ec2" fmt -check

printf 'wizard-flags\n3\n1\n1\n1\n2\n' | \
  "$work_dir/reaper" new --out "$work_dir/wizard" >/dev/null
test ! -e "$work_dir/wizard"
unzip -t "$work_dir/wizard.zip" >/dev/null

"$work_dir/reaper" serve --addr 127.0.0.1:18089 >"$work_dir/configurator.log" 2>&1 &
server_pid=$!
for _ in $(seq 1 50); do
  if curl --silent --fail http://127.0.0.1:18089/healthz >/dev/null; then
    break
  fi
  sleep 0.1
done
curl --silent --fail http://127.0.0.1:18089/ | rg -q 'Build and download repository'
curl --silent --fail \
  --header 'Content-Type: application/json' \
  --data '{"schema":"reaper.dev/v0alpha2","name":"browser-flags","capability":"feature-flags","service":{"language":"python"},"database":{"authority":"sqlite"},"deployment":{"target":"docker","replicas":1},"delivery":{"format":"zip"},"domain":{"tenant":"organization","targetingAttributes":["targetingKey","organization.id"]}}' \
  --output "$work_dir/browser-flags.zip" \
  http://127.0.0.1:18089/api/generate
unzip -t "$work_dir/browser-flags.zip" >/dev/null
unzip -l "$work_dir/browser-flags.zip" | rg -q 'browser-flags/reaper_flags/__main__.py'
kill "$server_pid"
wait "$server_pid" 2>/dev/null || true
server_pid=""

if "$work_dir/reaper" generate \
  --recipe recipes/python-postgres-kubernetes.yaml \
  --out "$work_dir/kubernetes" >/dev/null 2>&1; then
  echo "generator overwrote an existing output" >&2
  exit 1
fi

echo "SaaS Reaper product demo passed"
