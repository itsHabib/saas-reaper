#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
usage='usage: conformance.sh GENERATED_REPO_DIR LANGUAGE PORT'
generated_dir=${1:?$usage}
language=${2:?$usage}
port=${3:?$usage}

base_url=http://127.0.0.1:$port
admin_token=conformance-admin-token
evaluation_token=conformance-evaluation-token
work_dir=$(mktemp -d)
server_pid=''

cleanup() {
  if [[ -n "$server_pid" ]]; then
    kill "$server_pid" 2> /dev/null || true
    wait "$server_pid" 2> /dev/null || true
  fi
  if [[ "$work_dir" == /tmp/* || "$work_dir" == /var/folders/* ]]; then
    rm -rf -- "$work_dir"
  fi
}
trap cleanup EXIT

cd "$repo_dir"
(
  cd "$generated_dir"
  export PORT="$port"
  export REAPER_ADMIN_TOKEN="$admin_token"
  export REAPER_ADMIN_ACTOR=conformance
  export REAPER_EVALUATION_TOKEN="$evaluation_token"
  export DATABASE_PATH="$work_dir/flags.db"
  exec bash scripts/start-language.sh "$work_dir"
) > "$work_dir/server.log" 2>&1 &
server_pid=$!

ready=false
for _ in {1..300}; do
  if curl --fail --silent "$base_url/healthz" > /dev/null; then
    ready=true
    break
  fi
  sleep 0.1
done
if [[ "$ready" != true ]]; then
  echo "generated $language service did not become ready" >&2
  sed -n '1,120p' "$work_dir/server.log" >&2
  exit 1
fi

curl --fail-with-body --silent --show-error \
  -X PUT "$base_url/v1/environments/production/flags/checkout-v2" \
  -H "Authorization: Bearer $admin_token" \
  -H 'Content-Type: application/json' \
  --data-binary @fixtures/publish-checkout-v2.json > /dev/null

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
  client=$2
  value=$3
  variant=$4
  reason=$5
  actual=$(jq -r '[.language, (.value|tostring), .variant, .reason] | join("|")' <<< "$output")
  expected="$client|$value|$variant|$reason"
  if [[ "$actual" != "$expected" ]]; then
    echo "generated $language service, $client client mismatch: got $actual, want $expected" >&2
    exit 1
  fi
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

run_scenario user-2 acme true on TARGETING_MATCH
run_scenario user-1 other true on SPLIT
run_scenario user-2 other false off STATIC

echo "conformance: generated $language service matched the OpenFeature scenario table"
