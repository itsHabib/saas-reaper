#!/usr/bin/env bash
set -euo pipefail

repo_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
usage='usage: invariants.sh GENERATED_REPO_DIR LANGUAGE PORT'
generated_dir=${1:?$usage}
language=${2:?$usage}
port=${3:?$usage}

base_url=http://127.0.0.1:$port
admin_token=invariants-admin-token
evaluation_token=invariants-evaluation-token
work_dir=$(mktemp -d)
database_path=$work_dir/flags.db
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

fail() {
  echo "generated $language service broke an invariant: $1" >&2
  exit 1
}

boot_service() {
  (
    cd "$generated_dir"
    export PORT="$port"
    export REAPER_ADMIN_TOKEN="$admin_token"
    export REAPER_ADMIN_ACTOR=invariants
    export REAPER_EVALUATION_TOKEN="$evaluation_token"
    export DATABASE_PATH="$database_path"
    exec bash scripts/start-language.sh "$work_dir"
  ) >> "$work_dir/server.log" 2>&1 &
  server_pid=$!
  for _ in {1..300}; do
    if curl --fail --silent "$base_url/healthz" > /dev/null; then
      return 0
    fi
    sleep 0.1
  done
  echo "generated $language service did not become ready" >&2
  sed -n '1,120p' "$work_dir/server.log" >&2
  exit 1
}

stop_service() {
  kill "$server_pid" 2> /dev/null || true
  wait "$server_pid" 2> /dev/null || true
  server_pid=''
  for _ in {1..50}; do
    if ! curl --silent "$base_url/healthz" > /dev/null; then
      return 0
    fi
    sleep 0.1
  done
  fail "service kept answering after a stop signal; the port never drained"
}

publish_status() {
  token=$1
  key=$2
  body=$3
  curl --silent --output /dev/null --write-out '%{http_code}' \
    --request PUT \
    --header "Authorization: Bearer $token" \
    --header 'Content-Type: application/json' \
    --data "$body" \
    "$base_url/v1/environments/production/flags/$key"
}

evaluate_status() {
  token=$1
  header=(--header "Authorization: Bearer $token")
  if [[ -z "$token" ]]; then
    header=()
  fi
  curl --silent --output /dev/null --write-out '%{http_code}' \
    --request POST \
    "${header[@]}" \
    --header 'Content-Type: application/json' \
    --data '{"context":{"targetingKey":"user-2","organization.id":"acme"}}' \
    "$base_url/environments/production/ofrep/v1/evaluate/flags/checkout-v2"
}

audit_body() {
  curl --silent --fail \
    --header "Authorization: Bearer $admin_token" \
    "$base_url/v1/audit?limit=10"
}

assert_targeting_match() {
  result=$(curl --silent --fail \
    --request POST \
    --header "Authorization: Bearer $evaluation_token" \
    --header 'Content-Type: application/json' \
    --data '{"context":{"targetingKey":"user-2","organization.id":"acme"}}' \
    "$base_url/environments/production/ofrep/v1/evaluate/flags/checkout-v2")
  printf '%s' "$result" |
    jq -e '.value == true and .variant == "on" and .reason == "TARGETING_MATCH"' > /dev/null ||
    fail "$1"
}

fixture=$(cat fixtures/publish-checkout-v2.json)
update=$(jq -c '.expectedRevision = 1' <<< "$fixture")
minimal='{"expectedRevision":0,"flag":{"kind":"boolean","enabled":true,"defaultVariant":"off","variants":{"off":false,"on":true}}}'

boot_service

created=$(publish_status "$admin_token" checkout-v2 "$fixture")
[[ "$created" == 200 ]] || fail "initial publish returned $created, want 200"

stale=$(publish_status "$admin_token" checkout-v2 "$fixture")
[[ "$stale" == 409 ]] || fail "stale expectedRevision returned $stale, want 409"

publish_status "$admin_token" concurrent-flag "$minimal" > "$work_dir/race-one" &
race_one=$!
publish_status "$admin_token" concurrent-flag "$minimal" > "$work_dir/race-two" &
race_two=$!
wait "$race_one" "$race_two"
race_codes=$(sort "$work_dir/race-one" "$work_dir/race-two" | paste -sd ' ' -)
[[ "$race_codes" == "200 409" ]] ||
  fail "concurrent creation returned [$race_codes], want exactly one winner [200 409]"

write_with_read_token=$(publish_status "$evaluation_token" checkout-v2 "$update")
[[ "$write_with_read_token" == 401 ]] ||
  fail "evaluation token was allowed to publish ($write_with_read_token), want 401"

read_with_write_token=$(evaluate_status "$admin_token")
[[ "$read_with_write_token" == 401 ]] ||
  fail "management token was allowed to evaluate ($read_with_write_token), want 401"

read_with_no_token=$(evaluate_status "")
[[ "$read_with_no_token" == 401 ]] ||
  fail "missing token was allowed to evaluate ($read_with_no_token), want 401"

updated=$(publish_status "$admin_token" checkout-v2 "$update")
[[ "$updated" == 200 ]] || fail "revision-pinned update returned $updated, want 200"

audit_body | jq -e '
  (.audit | length == 3)
    and .audit[0].key == "checkout-v2" and .audit[0].revision == 2
    and (.audit | map(.actor) | unique == ["invariants"])
' > /dev/null || fail "audit does not hold one row per successful publish, newest first"

assert_targeting_match "pre-restart evaluation lost the published rule"

stop_service
boot_service

assert_targeting_match "restart lost the published definition"
audit_body | jq -e '.audit | length == 3' > /dev/null ||
  fail "restart lost audit rows"

echo "invariants: generated $language service held revision, authority, audit, and restart invariants"
