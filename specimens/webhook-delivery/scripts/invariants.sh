#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work_dir=$(mktemp -d)
database_path=$work_dir/webhooks.db
admin_token=invariants-management-token
read_token=invariants-attempt-read-token
server_pid=''
retry_delays=100ms,100ms
pids=()

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY=127.0.0.1,localhost
export no_proxy=$NO_PROXY

print_logs() {
  local log
  for log in "$work_dir"/*.log; do
    if [[ ! -f "$log" ]]; then
      continue
    fi
    echo "--- ${log##*/}" >&2
    sed -n '1,180p' "$log" >&2
  done
}

terminate_pid() {
  local pid=$1
  local guard_pid
  if ! kill -0 "$pid" 2> /dev/null; then
    wait "$pid" 2> /dev/null || true
    return
  fi
  kill "$pid" 2> /dev/null || true
  (
    sleep 3
    kill -9 "$pid" 2> /dev/null || true
  ) &
  guard_pid=$!
  wait "$pid" 2> /dev/null || true
  kill "$guard_pid" 2> /dev/null || true
  wait "$guard_pid" 2> /dev/null || true
}

cleanup() {
  local pid
  for pid in "${pids[@]}"; do
    terminate_pid "$pid"
  done
  if [[ "$work_dir" == /tmp/* || "$work_dir" == /var/folders/* ]]; then
    rm -rf -- "$work_dir"
  fi
}

finish() {
  local status=$?
  trap - EXIT
  if [[ "$status" -ne 0 ]]; then
    print_logs
  fi
  cleanup
  exit "$status"
}
trap finish EXIT
trap 'exit 130' INT TERM

fail() {
  echo "webhook invariant failed: $1" >&2
  exit 1
}

allocate_ports() {
  python3 - "$1" << 'PY'
import socket
import sys

sockets = []
for _ in range(int(sys.argv[1])):
    listener = socket.socket()
    listener.bind(("127.0.0.1", 0))
    sockets.append(listener)

print(" ".join(str(listener.getsockname()[1]) for listener in sockets))
PY
}

wait_ready() {
  local url=$1
  local label=$2
  local log=$3
  local _
  for _ in {1..240}; do
    if curl --fail --silent --max-time 1 "$url" > /dev/null; then
      return
    fi
    sleep 0.05
  done
  echo "$label did not become ready" >&2
  sed -n '1,180p' "$log" >&2
  exit 1
}

boot_service() {
  REAPER_WEBHOOK_ADDR="127.0.0.1:$service_port" \
    REAPER_WEBHOOK_DB=$database_path \
    REAPER_WEBHOOK_ADMIN_TOKEN=$admin_token \
    REAPER_WEBHOOK_ADMIN_ACTOR=invariants \
    REAPER_WEBHOOK_READ_TOKEN=$read_token \
    REAPER_WEBHOOK_RETRY_DELAYS=$retry_delays \
    REAPER_WEBHOOK_POLL_INTERVAL=25ms \
    REAPER_WEBHOOK_REQUEST_TIMEOUT=2s \
    "$work_dir/reaper-webhooks" >> "$work_dir/server.log" 2>&1 &
  server_pid=$!
  pids+=("$server_pid")
  wait_ready "$base_url/healthz" "webhook service" "$work_dir/server.log"
}

stop_service() {
  local index
  if [[ -z "$server_pid" ]]; then
    return
  fi
  terminate_pid "$server_pid"
  for index in "${!pids[@]}"; do
    if [[ "${pids[index]}" != "$server_pid" ]]; then
      continue
    fi
    unset 'pids[index]'
    break
  done
  server_pid=''
}

start_go_receiver() {
  local name=$1
  local port=$2
  local secret=$3
  local result=$4
  local fail_first=$5
  RECEIVER_ADDR="127.0.0.1:$port" \
    RECEIVER_SECRET=$secret \
    RECEIVER_RESULT=$result \
    RECEIVER_FAIL_FIRST=$fail_first \
    "$work_dir/receiver-go" > "$work_dir/$name-receiver.log" 2>&1 &
  pids+=("$!")
  wait_ready \
    "http://127.0.0.1:$port/health" \
    "$name receiver" \
    "$work_dir/$name-receiver.log"
}

register_endpoint() {
  local port=$1
  local result=$2
  jq -cn --arg url "http://127.0.0.1:$port/webhook" '{url: $url}' |
    curl --fail-with-body --silent --show-error \
      --request POST \
      --header "Authorization: Bearer $admin_token" \
      --header 'Content-Type: application/json' \
      --data-binary @- \
      "$base_url/v1/endpoints" > "$result"
  jq -e '
    (.id | startswith("ep_"))
      and (.secret | startswith("whsec_"))
      and .enabled == true
      and .revision == 1
  ' "$result" > /dev/null || fail "endpoint registration response was incomplete"
}

disable_endpoint() {
  local endpoint_id=$1
  local result=$2
  curl --fail-with-body --silent --show-error \
    --request POST \
    --header "Authorization: Bearer $admin_token" \
    --header 'Content-Type: application/json' \
    --data-binary '{"expectedRevision":1}' \
    "$base_url/v1/endpoints/$endpoint_id/disable" > "$result"
  jq -e \
    --arg endpoint "$endpoint_id" \
    '.id == $endpoint and .enabled == false and .revision == 2 and (has("secret") | not)' \
    "$result" > /dev/null || fail "endpoint disable did not return revision two without its secret"
}

publish_message() {
  local result=$1
  curl --fail-with-body --silent --show-error \
    --request POST \
    --header "Authorization: Bearer $admin_token" \
    --header 'Content-Type: application/json' \
    --data-binary @fixtures/message.json \
    "$base_url/v1/messages" > "$result"
  jq -e '(.messageId | startswith("msg_")) and (.deliveryIds | type == "array")' \
    "$result" > /dev/null || fail "publication response was incomplete"
}

attempts_for() {
  local message_id=$1
  local endpoint_id=${2:-}
  local query=(
    --data-urlencode "messageId=$message_id"
    --data-urlencode 'limit=1000'
  )
  if [[ -n "$endpoint_id" ]]; then
    query+=(--data-urlencode "endpointId=$endpoint_id")
  fi
  curl --fail --silent --show-error --get \
    --header "Authorization: Bearer $read_token" \
    "${query[@]}" \
    "$base_url/v1/attempts"
}

wait_attempts() {
  local message_id=$1
  local endpoint_id=$2
  local expected=$3
  local result=$4
  local _
  for _ in {1..360}; do
    if attempts_for "$message_id" "$endpoint_id" > "$result" 2> /dev/null &&
      jq -e --argjson expected "$expected" '.attempts | length >= $expected' "$result" > /dev/null; then
      return
    fi
    sleep 0.05
  done
  fail "attempt audit for $message_id did not reach $expected row(s)"
}

wait_receipts() {
  local result=$1
  local expected=$2
  local label=$3
  local _
  for _ in {1..360}; do
    if [[ -f "$result" ]] &&
      jq -s -e --argjson expected "$expected" 'length >= $expected' "$result" > /dev/null 2>&1; then
      return
    fi
    sleep 0.05
  done
  fail "$label did not record $expected receipt(s)"
}

response_status() {
  curl --silent --output /dev/null --write-out '%{http_code}' "$@"
}

cd "$specimen_dir"
GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/reaper-webhooks" ./cmd/reaper-webhooks
GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/receiver-go" ./cmd/receiver-go

port_line=$(allocate_ports 5)
read -r service_port retry_port disabled_port replay_port restart_port <<< "$port_line"
base_url=http://127.0.0.1:$service_port
fixture_payload_base64=$(base64 < fixtures/message.json | tr -d '\n')

boot_service

read_with_management=$(response_status \
  --header "Authorization: Bearer $admin_token" \
  "$base_url/v1/attempts")
[[ "$read_with_management" == 401 ]] ||
  fail "management token read attempt audit with status $read_with_management, want 401"

publish_with_read=$(response_status \
  --request POST \
  --header "Authorization: Bearer $read_token" \
  --header 'Content-Type: application/json' \
  --data-binary @fixtures/message.json \
  "$base_url/v1/messages")
[[ "$publish_with_read" == 401 ]] ||
  fail "read token published a message with status $publish_with_read, want 401"

read_with_read=$(response_status \
  --header "Authorization: Bearer $read_token" \
  "$base_url/v1/attempts")
[[ "$read_with_read" == 200 ]] ||
  fail "read token could not read attempt audit: status $read_with_read"
echo "token separation: management cannot read and read authority cannot publish"

register_endpoint "$retry_port" "$work_dir/retry-endpoint.json"
retry_endpoint=$(jq -er '.id' "$work_dir/retry-endpoint.json")
start_go_receiver \
  retry \
  "$retry_port" \
  "$(jq -er '.secret' "$work_dir/retry-endpoint.json")" \
  "$work_dir/retry-receipts.jsonl" \
  2
publish_message "$work_dir/retry-publication.json"
retry_message=$(jq -er '.messageId' "$work_dir/retry-publication.json")
jq -e '.deliveryIds | length == 1' "$work_dir/retry-publication.json" > /dev/null ||
  fail "retry publication did not queue exactly one delivery"
wait_receipts "$work_dir/retry-receipts.jsonl" 3 "retry receiver"
wait_attempts "$retry_message" "$retry_endpoint" 3 "$work_dir/retry-attempts.json"

jq -s -e --arg message "$retry_message" --arg payload "$fixture_payload_base64" '
  length == 3
    and [.[].attempt] == [1, 2, 3]
    and (all(.[];
      .messageId == $message
        and .payloadBase64 == $payload
        and .accepted == true
        and .tamperedRejected == true))
' "$work_dir/retry-receipts.jsonl" > /dev/null || fail "retry receipts did not preserve signed identity and payload"
jq -e '
  ([.attempts[]] | sort_by(.number)) as $rows
    | ($rows | length == 3)
      and [$rows[].number] == [1, 2, 3]
      and [$rows[].outcome] == ["retrying", "retrying", "delivered"]
      and [$rows[].statusCode] == [500, 500, 204]
      and ($rows[0].nextAttemptAt != null)
      and ($rows[1].nextAttemptAt != null)
      and ($rows[2] | has("nextAttemptAt") | not)
      and ($rows[0].sequence < $rows[1].sequence)
      and ($rows[1].sequence < $rows[2].sequence)
' "$work_dir/retry-attempts.json" > /dev/null || fail "two failures were not durably audited before retry success"
disable_endpoint "$retry_endpoint" "$work_dir/retry-disabled.json"
echo "retry invariant: two injected 500s were audited before delivery succeeded"

register_endpoint "$disabled_port" "$work_dir/disabled-endpoint.json"
disabled_endpoint=$(jq -er '.id' "$work_dir/disabled-endpoint.json")
start_go_receiver \
  disabled \
  "$disabled_port" \
  "$(jq -er '.secret' "$work_dir/disabled-endpoint.json")" \
  "$work_dir/disabled-receipts.jsonl" \
  0
disable_endpoint "$disabled_endpoint" "$work_dir/disabled-response.json"
publish_message "$work_dir/disabled-publication.json"
disabled_message=$(jq -er '.messageId' "$work_dir/disabled-publication.json")
jq -e '.deliveryIds | length == 0' "$work_dir/disabled-publication.json" > /dev/null ||
  fail "disabled endpoint still received queued work"
sleep 0.4
[[ ! -s "$work_dir/disabled-receipts.jsonl" ]] || fail "disabled endpoint received an HTTP request"
attempts_for "$disabled_message" "$disabled_endpoint" > "$work_dir/disabled-attempts.json"
jq -e '.attempts | length == 0' "$work_dir/disabled-attempts.json" > /dev/null ||
  fail "disabled endpoint produced an attempt row"
echo "disabled endpoint invariant: disable-before-publish remained silent"

register_endpoint "$replay_port" "$work_dir/replay-endpoint.json"
replay_endpoint=$(jq -er '.id' "$work_dir/replay-endpoint.json")
start_go_receiver \
  replay \
  "$replay_port" \
  "$(jq -er '.secret' "$work_dir/replay-endpoint.json")" \
  "$work_dir/replay-receipts.jsonl" \
  0
publish_message "$work_dir/replay-publication.json"
replay_message=$(jq -er '.messageId' "$work_dir/replay-publication.json")
jq -e '.deliveryIds | length == 1' "$work_dir/replay-publication.json" > /dev/null ||
  fail "replay source publication did not queue one delivery"
wait_receipts "$work_dir/replay-receipts.jsonl" 1 "replay receiver"
wait_attempts "$replay_message" "$replay_endpoint" 1 "$work_dir/replay-before.json"

curl --fail-with-body --silent --show-error \
  --request POST \
  --header "Authorization: Bearer $admin_token" \
  "$base_url/v1/messages/$replay_message/replay/$replay_endpoint" > "$work_dir/replay-response.json"
jq -e --arg message "$replay_message" '
  .messageId == $message and (.deliveryIds | length == 1)
' "$work_dir/replay-response.json" > /dev/null || fail "replay did not retain the source message ID"
wait_receipts "$work_dir/replay-receipts.jsonl" 2 "replay receiver"
wait_attempts "$replay_message" "$replay_endpoint" 2 "$work_dir/replay-after.json"

jq -s -e --arg message "$replay_message" --arg payload "$fixture_payload_base64" '
  length == 2
    and (all(.[];
      .messageId == $message
        and .payloadBase64 == $payload
        and .accepted == true
        and .tamperedRejected == true))
' "$work_dir/replay-receipts.jsonl" > /dev/null || fail "replay changed webhook identity or exact payload"
jq -e --arg message "$replay_message" --arg endpoint "$replay_endpoint" '
  (.attempts | length == 2)
    and (all(.attempts[];
      .messageId == $message
        and .endpointId == $endpoint
        and .number == 1
        and .outcome == "delivered"))
    and ([.attempts[].deliveryId] | unique | length == 2)
' "$work_dir/replay-after.json" > /dev/null || fail "replay did not create a distinct successful delivery"
disable_endpoint "$replay_endpoint" "$work_dir/replay-disabled.json"
echo "replay invariant: distinct deliveries retained the original webhook-id"

stop_service
retry_delays=3s,100ms
boot_service
register_endpoint "$restart_port" "$work_dir/restart-endpoint.json"
restart_endpoint=$(jq -er '.id' "$work_dir/restart-endpoint.json")
start_go_receiver \
  restart \
  "$restart_port" \
  "$(jq -er '.secret' "$work_dir/restart-endpoint.json")" \
  "$work_dir/restart-receipts.jsonl" \
  1
publish_message "$work_dir/restart-publication.json"
restart_message=$(jq -er '.messageId' "$work_dir/restart-publication.json")
jq -e '.deliveryIds | length == 1' "$work_dir/restart-publication.json" > /dev/null ||
  fail "restart publication did not queue one delivery"
wait_receipts "$work_dir/restart-receipts.jsonl" 1 "restart receiver"
wait_attempts "$restart_message" "$restart_endpoint" 1 "$work_dir/restart-before.json"
jq -e '
  (.attempts | length == 1)
    and .attempts[0].number == 1
    and .attempts[0].outcome == "retrying"
    and .attempts[0].statusCode == 500
    and (.attempts[0].nextAttemptAt != null)
' "$work_dir/restart-before.json" > /dev/null || fail "restart setup did not persist one pending retry and its audit"
restart_first_before=$(jq -cS '.attempts[0]' "$work_dir/restart-before.json")

stop_service
boot_service
wait_receipts "$work_dir/restart-receipts.jsonl" 2 "restart receiver"
wait_attempts "$restart_message" "$restart_endpoint" 2 "$work_dir/restart-after.json"
jq -s -e --arg message "$restart_message" --arg payload "$fixture_payload_base64" '
  length == 2
    and (all(.[];
      .messageId == $message
        and .payloadBase64 == $payload
        and .accepted == true
        and .tamperedRejected == true))
' "$work_dir/restart-receipts.jsonl" > /dev/null || fail "restart changed webhook identity or fixture bytes"
restart_first_after=$(jq -cS '.attempts[] | select(.number == 1)' "$work_dir/restart-after.json")
[[ "$restart_first_after" == "$restart_first_before" ]] || fail "first attempt audit changed across restart"
jq -e '
  ([.attempts[]] | sort_by(.number)) as $rows
    | ($rows | length == 2)
      and [$rows[].number] == [1, 2]
      and [$rows[].outcome] == ["retrying", "delivered"]
      and [$rows[].statusCode] == [500, 204]
      and ($rows[0].sequence < $rows[1].sequence)
' "$work_dir/restart-after.json" > /dev/null || fail "pending delivery did not resume successfully after restart"

curl --fail --silent --show-error \
  --header "Authorization: Bearer $read_token" \
  "$base_url/v1/attempts?limit=1000" > "$work_dir/all-attempts.json"
jq -e '
  (.attempts | length == 7)
    and (all(.attempts[]; .actor == "invariants"))
    and ([.attempts[].sequence] | unique | length == 7)
' "$work_dir/all-attempts.json" > /dev/null || fail "append-only audit lost or duplicated rows across restart"
echo "restart invariant: pending work and append-only audit survived the same SQLite authority restart"

echo "webhook invariants: retry, disable, replay, restart durability, and token separation verified"
