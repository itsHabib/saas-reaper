#!/usr/bin/env bash
set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work_dir=$(mktemp -d)
admin_token=demo-management-token
read_token=demo-attempt-read-token
server_pid=''
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
    sed -n '1,160p' "$log" >&2
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
  for pid in ${pids[@]+"${pids[@]}"}; do
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
  echo "webhook demo failed: $1" >&2
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
  for _ in {1..200}; do
    if curl --fail --silent --max-time 1 "$url" > /dev/null; then
      return
    fi
    sleep 0.05
  done
  echo "$label did not become ready" >&2
  sed -n '1,160p' "$log" >&2
  exit 1
}

wait_receipts() {
  local result=$1
  local expected=$2
  local label=$3
  local _
  for _ in {1..300}; do
    if [[ -f "$result" ]] &&
      jq -s -e --argjson expected "$expected" 'length >= $expected' "$result" > /dev/null 2>&1; then
      return
    fi
    sleep 0.05
  done
  fail "$label did not record $expected receipt(s)"
}

attempts_for() {
  local message_id=$1
  curl --fail --silent --show-error --get \
    --header "Authorization: Bearer $read_token" \
    --data-urlencode "messageId=$message_id" \
    --data-urlencode 'limit=1000' \
    "$base_url/v1/attempts"
}

wait_attempts() {
  local message_id=$1
  local expected=$2
  local result=$3
  local _
  for _ in {1..300}; do
    if attempts_for "$message_id" > "$result" 2> /dev/null &&
      jq -e --argjson expected "$expected" '.attempts | length >= $expected' "$result" > /dev/null; then
      return
    fi
    sleep 0.05
  done
  fail "attempt audit did not reach $expected row(s)"
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

start_go_receiver() {
  local port=$1
  local secret=$2
  local result=$3
  RECEIVER_ADDR="127.0.0.1:$port" \
    RECEIVER_SECRET=$secret \
    RECEIVER_RESULT=$result \
    "$work_dir/receiver-go" > "$work_dir/go-receiver.log" 2>&1 &
  pids+=("$!")
  wait_ready "http://127.0.0.1:$port/health" "Go receiver" "$work_dir/go-receiver.log"
}

start_typescript_receiver() {
  local port=$1
  local secret=$2
  local result=$3
  (
    cd "$specimen_dir/receivers/typescript"
    RECEIVER_ADDR="127.0.0.1:$port" \
      RECEIVER_SECRET=$secret \
      RECEIVER_RESULT=$result \
      exec ./node_modules/.bin/tsx receiver.ts
  ) > "$work_dir/typescript-receiver.log" 2>&1 &
  pids+=("$!")
  wait_ready \
    "http://127.0.0.1:$port/health" \
    "TypeScript receiver" \
    "$work_dir/typescript-receiver.log"
}

start_python_receiver() {
  local port=$1
  local secret=$2
  local result=$3
  RECEIVER_ADDR="127.0.0.1:$port" \
    RECEIVER_SECRET=$secret \
    RECEIVER_RESULT=$result \
    "$specimen_dir/receivers/python/.venv/bin/python" \
    "$specimen_dir/receivers/python/receiver.py" > "$work_dir/python-receiver.log" 2>&1 &
  pids+=("$!")
  wait_ready \
    "http://127.0.0.1:$port/health" \
    "Python receiver" \
    "$work_dir/python-receiver.log"
}

cd "$specimen_dir"
if [[ ! -x receivers/typescript/node_modules/.bin/tsx ]]; then
  fail "TypeScript receiver dependencies are missing; run make setup"
fi
if [[ ! -x receivers/python/.venv/bin/python ]]; then
  fail "Python receiver dependencies are missing; run make setup"
fi

GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/reaper-webhooks" ./cmd/reaper-webhooks
GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/receiver-go" ./cmd/receiver-go

port_line=$(allocate_ports 4)
read -r service_port go_port typescript_port python_port <<< "$port_line"
base_url=http://127.0.0.1:$service_port

REAPER_WEBHOOK_ADDR="127.0.0.1:$service_port" \
  REAPER_WEBHOOK_DB="$work_dir/webhooks.db" \
  REAPER_WEBHOOK_ADMIN_TOKEN=$admin_token \
  REAPER_WEBHOOK_ADMIN_ACTOR=demo \
  REAPER_WEBHOOK_READ_TOKEN=$read_token \
  REAPER_WEBHOOK_RETRY_DELAYS=100ms,100ms \
  REAPER_WEBHOOK_POLL_INTERVAL=25ms \
  REAPER_WEBHOOK_REQUEST_TIMEOUT=2s \
  "$work_dir/reaper-webhooks" > "$work_dir/server.log" 2>&1 &
server_pid=$!
pids+=("$server_pid")
wait_ready "$base_url/healthz" "webhook service" "$work_dir/server.log"

register_endpoint "$go_port" "$work_dir/go-endpoint.json"
register_endpoint "$typescript_port" "$work_dir/typescript-endpoint.json"
register_endpoint "$python_port" "$work_dir/python-endpoint.json"

start_go_receiver \
  "$go_port" \
  "$(jq -er '.secret' "$work_dir/go-endpoint.json")" \
  "$work_dir/go-receipts.jsonl"
start_typescript_receiver \
  "$typescript_port" \
  "$(jq -er '.secret' "$work_dir/typescript-endpoint.json")" \
  "$work_dir/typescript-receipts.jsonl"
start_python_receiver \
  "$python_port" \
  "$(jq -er '.secret' "$work_dir/python-endpoint.json")" \
  "$work_dir/python-receipts.jsonl"

curl --fail-with-body --silent --show-error \
  --request POST \
  --header "Authorization: Bearer $admin_token" \
  --header 'Content-Type: application/json' \
  --data-binary @fixtures/message.json \
  "$base_url/v1/messages" > "$work_dir/publication.json"

message_id=$(jq -er '.messageId' "$work_dir/publication.json")
jq -e '
  (.messageId | startswith("msg_"))
    and (.deliveryIds | length == 3)
    and (.deliveryIds | unique | length == 3)
' "$work_dir/publication.json" > /dev/null || fail "publication did not queue three unique deliveries"

wait_receipts "$work_dir/go-receipts.jsonl" 1 "Go verifier"
wait_receipts "$work_dir/typescript-receipts.jsonl" 1 "TypeScript verifier"
wait_receipts "$work_dir/python-receipts.jsonl" 1 "Python verifier"
wait_attempts "$message_id" 3 "$work_dir/attempts.json"

expected_payload_base64=$(base64 < fixtures/message.json | tr -d '\n')
for language in go typescript python; do
  result=$work_dir/$language-receipts.jsonl
  jq -s -e --arg message "$message_id" --arg payload "$expected_payload_base64" '
    length == 1
      and .[0].messageId == $message
      and .[0].payloadBase64 == $payload
      and .[0].accepted == true
      and .[0].tamperedRejected == true
      and (.[0].timestamp | test("^[0-9]+$"))
  ' "$result" > /dev/null || fail "$language official verifier proof did not match the fixture bytes"
done

jq -e --arg message "$message_id" --slurpfile publication "$work_dir/publication.json" '
  (.attempts | length == 3)
    and (all(.attempts[];
      .messageId == $message
        and .actor == "demo"
        and .number == 1
        and .outcome == "delivered"
        and .statusCode == 204
        and (.webhookTimestamp > 0)))
    and ([.attempts[].endpointId] | unique | length == 3)
    and ([.attempts[].deliveryId] | sort == ($publication[0].deliveryIds | sort))
' "$work_dir/attempts.json" > /dev/null || fail "durable attempt audit did not match the three deliveries"

echo "Go official verifier: accepted exact delivery and rejected same-length signature tamper"
echo "JavaScript official verifier: accepted exact delivery and rejected same-length signature tamper"
echo "Python official verifier: accepted exact delivery and rejected same-length signature tamper"
echo "webhook demo: three real deliveries and three durable delivered attempts verified"
