#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
demo_dir=$(mktemp -d)
server_pid=''
base_url=http://127.0.0.1:18080
admin_token=demo-admin-token
evaluation_token=demo-evaluation-token

cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2>/dev/null || true
    wait "$server_pid" 2>/dev/null || true
  fi
  if [[ "$demo_dir" == /tmp/* || "$demo_dir" == /var/folders/* ]]; then
    rm -rf -- "$demo_dir"
  fi
}
trap cleanup EXIT

cd "$repo_dir"
REAPER_ADMIN_TOKEN=$admin_token \
REAPER_ADMIN_ACTOR=demo \
REAPER_EVALUATION_TOKEN=$evaluation_token \
REAPER_DB="$demo_dir/flags.db" \
REAPER_ADDR=127.0.0.1:18080 \
  go run ./cmd/reaper-flags >"$demo_dir/server.log" 2>&1 &
server_pid=$!

ready=false
for _ in {1..100}; do
  if curl --fail --silent "$base_url/healthz" >/dev/null; then
    ready=true
    break
  fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then
  echo "server did not become ready" >&2
  sed -n '1,120p' "$demo_dir/server.log" >&2
  exit 1
fi

curl --fail-with-body --silent --show-error \
  -X PUT "$base_url/v1/environments/production/flags/checkout-v2" \
  -H "Authorization: Bearer $admin_token" \
  -H 'Content-Type: application/json' \
  --data-binary @fixtures/publish-checkout-v2.json >/dev/null

run_go() {
  OFREP_ENDPOINT="$base_url/environments/production" \
  REAPER_EVALUATION_TOKEN=$evaluation_token \
  TARGETING_KEY=$1 \
  ORGANIZATION_ID=$2 \
    go run ./examples/go
}

run_typescript() {
  OFREP_ENDPOINT="$base_url/environments/production" \
  REAPER_EVALUATION_TOKEN=$evaluation_token \
  TARGETING_KEY=$1 \
  ORGANIZATION_ID=$2 \
  NODE_NO_WARNINGS=1 \
    npm --prefix examples/typescript run --silent start
}

run_python() {
  OFREP_ENDPOINT="$base_url/environments/production" \
  REAPER_EVALUATION_TOKEN=$evaluation_token \
  TARGETING_KEY=$1 \
  ORGANIZATION_ID=$2 \
    examples/python/.venv/bin/python examples/python/client.py
}

assert_result() {
  output=$1
  language=$2
  value=$3
  variant=$4
  reason=$5
  actual=$(jq -r '[.language, (.value|tostring), .variant, .reason] | join("|")' <<<"$output")
  expected="$language|$value|$variant|$reason"
  if [[ "$actual" != "$expected" ]]; then
    echo "client mismatch: got $actual, want $expected" >&2
    exit 1
  fi
  echo "$output"
}

run_scenario() {
  targeting_key=$1
  organization_id=$2
  expected_value=$3
  expected_variant=$4
  expected_reason=$5

  assert_result "$(run_go "$targeting_key" "$organization_id")" go "$expected_value" "$expected_variant" "$expected_reason"
  assert_result "$(run_typescript "$targeting_key" "$organization_id")" typescript "$expected_value" "$expected_variant" "$expected_reason"
  assert_result "$(run_python "$targeting_key" "$organization_id")" python "$expected_value" "$expected_variant" "$expected_reason"
}

echo "rule match: organization acme"
run_scenario user-2 acme true on TARGETING_MATCH

echo "rollout match: user-1 is in the stable 30 percent bucket"
run_scenario user-1 other true on SPLIT

echo "rollout miss: user-2 is outside the stable 30 percent bucket"
run_scenario user-2 other false off STATIC

echo "audit trail"
curl --fail --silent \
  "$base_url/v1/audit?limit=10" \
  -H "Authorization: Bearer $admin_token" \
  | jq -c '.audit | map({sequence, key, revision, actor})'
