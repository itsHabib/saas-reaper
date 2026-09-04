#!/usr/bin/env bash
# Shared local-proof harness for the notification routing specimen.
# Sourced by demo.sh and invariants.sh; it starts nothing on its own.

set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work_dir=$(mktemp -d)
admin_token=${admin_token:-proof-management-token}
read_token=${read_token:-proof-audit-read-token}
admin_actor=${admin_actor:-proof}
retry_delays=${retry_delays:-100ms,100ms}
proof_label=${proof_label:-notification proof}
service_port=19400
smtp_port=19401
server_pid=''
pids=()

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY=127.0.0.1,localhost
export no_proxy=$NO_PROXY

base_url=http://127.0.0.1:$service_port

print_logs() {
  local artifact
  for artifact in "$work_dir"/*.log "$work_dir"/*.jsonl; do
    if [[ ! -f "$artifact" ]]; then
      continue
    fi
    echo "--- ${artifact##*/}" >&2
    sed -n '1,180p' "$artifact" >&2
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
  # bash 3.2 aborts on "${pids[@]}" when the array is empty under set -u.
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

fail() {
  echo "$proof_label failed: $1" >&2
  exit 1
}

require_free_ports() {
  local port
  for port in "$@"; do
    if bash -c "exec 3<>/dev/tcp/127.0.0.1/$port" 2> /dev/null; then
      fail "loopback port $port is already in use"
    fi
  done
}

build_binaries() {
  cd "$specimen_dir"
  GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/reaper-notifications" ./cmd/reaper-notifications
  GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/sink-smtp" ./cmd/sink-smtp
  GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/sink-slack" ./cmd/sink-slack
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

wait_file() {
  local path=$1
  local label=$2
  local log=$3
  local _
  for _ in {1..240}; do
    if [[ -s "$path" ]]; then
      return
    fi
    sleep 0.05
  done
  echo "$label did not become ready" >&2
  sed -n '1,180p' "$log" >&2
  exit 1
}

start_smtp_sink() {
  local name=$1
  local port=$2
  local result=$3
  local fail_first=$4
  SINK_ADDR="127.0.0.1:$port" \
    SINK_RESULT=$result \
    SINK_READY="$work_dir/$name-smtp.ready" \
    SINK_FAIL_FIRST=$fail_first \
    "$work_dir/sink-smtp" > "$work_dir/$name-smtp-sink.log" 2>&1 &
  pids+=("$!")
  wait_file "$work_dir/$name-smtp.ready" "$name SMTP sink" "$work_dir/$name-smtp-sink.log"
}

start_slack_sink() {
  local name=$1
  local port=$2
  local result=$3
  local fail_first=$4
  SINK_ADDR="127.0.0.1:$port" \
    SINK_RESULT=$result \
    SINK_FAIL_FIRST=$fail_first \
    "$work_dir/sink-slack" > "$work_dir/$name-slack-sink.log" 2>&1 &
  pids+=("$!")
  wait_ready "http://127.0.0.1:$port/health" "$name Slack sink" "$work_dir/$name-slack-sink.log"
}

boot_service() {
  REAPER_NOTIFY_ADDR="127.0.0.1:$service_port" \
    REAPER_NOTIFY_DB="$work_dir/notifications.db" \
    REAPER_NOTIFY_ADMIN_TOKEN=$admin_token \
    REAPER_NOTIFY_ADMIN_ACTOR=$admin_actor \
    REAPER_NOTIFY_READ_TOKEN=$read_token \
    REAPER_NOTIFY_SMTP_ADDR="127.0.0.1:$smtp_port" \
    REAPER_NOTIFY_SMTP_FROM=notifications@reaper.example \
    REAPER_NOTIFY_RETRY_DELAYS=$retry_delays \
    REAPER_NOTIFY_POLL_INTERVAL=25ms \
    REAPER_NOTIFY_REQUEST_TIMEOUT=2s \
    "$work_dir/reaper-notifications" >> "$work_dir/server.log" 2>&1 &
  server_pid=$!
  pids+=("$server_pid")
  wait_ready "$base_url/healthz" "notification service" "$work_dir/server.log"
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

management_post() {
  local path=$1
  local body=$2
  local result=$3
  printf '%s' "$body" |
    curl --fail-with-body --silent --show-error \
      --request POST \
      --header "Authorization: Bearer $admin_token" \
      --header 'Content-Type: application/json' \
      --data-binary @- \
      "$base_url$path" > "$result"
}

register_channel() {
  local id=$1
  local kind=$2
  management_post /v1/channels \
    "$(printf '{"id":"%s","kind":"%s"}' "$id" "$kind")" \
    "$work_dir/channel-$id.json"
  jq -e --arg id "$id" '.id == $id and .enabled == true and .revision == 1' \
    "$work_dir/channel-$id.json" > /dev/null || fail "channel $id registration was incomplete"
}

disable_channel() {
  local id=$1
  management_post "/v1/channels/$id/disable" '{"expectedRevision":1}' "$work_dir/channel-$id-disabled.json"
  jq -e --arg id "$id" '.id == $id and .enabled == false and .revision == 2' \
    "$work_dir/channel-$id-disabled.json" > /dev/null || fail "channel $id disable did not revision to two"
}

create_template() {
  local key=$1
  local channel=$2
  local subject=$3
  local body=$4
  management_post /v1/templates \
    "$(jq -cn --arg key "$key" --arg channel "$channel" --arg subject "$subject" --arg body "$body" \
      '{key: $key, channel: $channel, subject: $subject, body: $body}')" \
    "$work_dir/template-$key-$channel.json"
}

create_recipient() {
  local id=$1
  local body=$2
  management_post /v1/recipients "$body" "$work_dir/recipient-$id.json"
}

# send_notification embeds the fixture bytes literally so the harness never reserializes
# the payload numbers it is about to assert on.
send_notification() {
  local key=$1
  local recipient=$2
  local result=$3
  local payload_file=${4:-$specimen_dir/fixtures/payload.json}
  management_post /v1/notifications \
    "$(printf '{"template":"invoice-paid","recipient":"%s","idempotencyKey":"%s","payload":%s}' \
      "$recipient" "$key" "$(cat "$payload_file")")" \
    "$result"
}

send_status() {
  local key=$1
  local recipient=$2
  local payload=$3
  printf '{"template":"invoice-paid","recipient":"%s","idempotencyKey":"%s","payload":%s}' \
    "$recipient" "$key" "$payload" |
    curl --silent --output "$work_dir/send-status-body.json" --write-out '%{http_code}' \
      --request POST \
      --header "Authorization: Bearer $admin_token" \
      --header 'Content-Type: application/json' \
      --data-binary @- \
      "$base_url/v1/notifications"
}

attempts_for() {
  local notification_id=$1
  local channel_id=${2:-}
  local query=(
    --data-urlencode "notificationId=$notification_id"
    --data-urlencode 'limit=1000'
  )
  if [[ -n "$channel_id" ]]; then
    query+=(--data-urlencode "channelId=$channel_id")
  fi
  curl --fail --silent --show-error --get \
    --header "Authorization: Bearer $read_token" \
    "${query[@]}" \
    "$base_url/v1/attempts"
}

wait_attempts() {
  local notification_id=$1
  local expected=$2
  local result=$3
  local _
  for _ in {1..400}; do
    if attempts_for "$notification_id" > "$result" 2> /dev/null &&
      jq -e --argjson expected "$expected" '.attempts | length >= $expected' "$result" > /dev/null; then
      return
    fi
    sleep 0.05
  done
  fail "attempt audit for $notification_id did not reach $expected row(s)"
}

wait_receipts() {
  local result=$1
  local expected=$2
  local label=$3
  local _
  for _ in {1..400}; do
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
