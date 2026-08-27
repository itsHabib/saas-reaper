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
    kill -9 "$server_pid" 2> /dev/null || true
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
  drained=false
  for _ in {1..50}; do
    if ! curl --silent "$base_url/healthz" > /dev/null; then
      drained=true
      break
    fi
    sleep 0.1
  done
  if [[ "$drained" != true ]]; then
    kill -9 "$server_pid" 2> /dev/null || true
    wait "$server_pid" 2> /dev/null || true
    server_pid=''
    fail "service kept serving for five seconds after a stop signal"
  fi
  (
    sleep 5
    kill -9 "$server_pid" 2> /dev/null
  ) &
  reap_deadline=$!
  wait "$server_pid" 2> /dev/null || true
  server_pid=''
  kill "$reap_deadline" 2> /dev/null || true
  wait "$reap_deadline" 2> /dev/null || true
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

evaluate_organization() {
  curl --silent --fail \
    --request POST \
    --header "Authorization: Bearer $evaluation_token" \
    --header 'Content-Type: application/json' \
    --data "{\"context\":{\"targetingKey\":\"user-2\",\"organization.id\":\"$1\"}}" \
    "$base_url/environments/production/ofrep/v1/evaluate/flags/checkout-v2"
}

assert_rule_match() {
  evaluate_organization "$1" |
    jq -e '.value == true and .variant == "on" and .reason == "TARGETING_MATCH"' > /dev/null ||
    fail "$2"
}

assert_rollout_miss() {
  evaluate_organization "$1" |
    jq -e '.value == false and .variant == "off" and .reason == "STATIC"' > /dev/null ||
    fail "$2"
}

audit_rows() {
  curl --silent --fail \
    --header "Authorization: Bearer $admin_token" \
    "$base_url/v1/audit?limit=10" | jq -cS '.audit'
}

fixture=$(cat fixtures/publish-checkout-v2.json)
update=$(jq -c '.expectedRevision = 1 | .flag.rules[0].equals = "umbrella"' <<< "$fixture")
minimal='{"expectedRevision":0,"flag":{"kind":"boolean","enabled":true,"defaultVariant":"off","variants":{"off":false,"on":true}}}'

boot_service

created=$(publish_status "$admin_token" checkout-v2 "$fixture")
[[ "$created" == 200 ]] || fail "initial publish returned $created, want 200"

stale=$(publish_status "$admin_token" checkout-v2 "$fixture")
[[ "$stale" == 409 ]] || fail "stale expectedRevision returned $stale, want 409"

starting_gun=$work_dir/race-gun
(
  until [[ -f "$starting_gun" ]]; do sleep 0.01; done
  publish_status "$admin_token" concurrent-flag "$minimal"
) > "$work_dir/race-one" &
race_one=$!
(
  until [[ -f "$starting_gun" ]]; do sleep 0.01; done
  publish_status "$admin_token" concurrent-flag "$minimal"
) > "$work_dir/race-two" &
race_two=$!
sleep 0.3
touch "$starting_gun"
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

assert_rule_match umbrella "revision-2 rule change is not being served"
assert_rollout_miss acme "revision-1 rule is still matching after the revision-2 update"

audit_before=$(audit_rows)
jq -e '
  length == 3
    and .[0].key == "checkout-v2" and .[0].revision == 2
    and .[1].key == "concurrent-flag" and .[1].revision == 1
    and .[2].key == "checkout-v2" and .[2].revision == 1
    and .[0].sequence > .[1].sequence and .[1].sequence > .[2].sequence
    and (map(.actor) | unique == ["invariants"])
' <<< "$audit_before" > /dev/null ||
  fail "audit does not hold one row per successful publish in newest-first publication order"

stop_service
boot_service

assert_rule_match umbrella "restart lost the latest published definition"
assert_rollout_miss acme "restart resurrected a superseded definition"

audit_after=$(audit_rows)
[[ "$audit_after" == "$audit_before" ]] ||
  fail "audit rows changed across restart"

echo "invariants: generated $language service held revision, authority, audit, and restart invariants"
