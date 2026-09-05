#!/usr/bin/env bash
# Shared local-proof harness for the ingress-tunnel specimen.
# Sourced by demo.sh and invariants.sh; it starts nothing on its own.

set -euo pipefail

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work_dir=$(mktemp -d)
admin_token=${admin_token:-proof-management-token}
read_token=${read_token:-proof-read-token}
admin_actor=${admin_actor:-proof}
proof_label=${proof_label:-tunnel proof}
domain=tunnel.test
control_port=19500
edge_port=19501
# shellcheck disable=SC2034 # the sourcing proofs pick their origin ports from here
acme_target_port=19502
# shellcheck disable=SC2034
umbrella_target_port=19503
server_pid=''
pids=()

unset HTTP_PROXY HTTPS_PROXY ALL_PROXY http_proxy https_proxy all_proxy
export NO_PROXY=127.0.0.1,localhost
export no_proxy=$NO_PROXY

control_url=http://127.0.0.1:$control_port
edge_url=http://127.0.0.1:$edge_port

print_logs() {
  local artifact
  for artifact in "$work_dir"/*.log; do
    if [[ ! -f "$artifact" ]]; then
      continue
    fi
    echo "--- ${artifact##*/}" >&2
    sed -n '1,120p' "$artifact" >&2
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
  GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/reaper-tunnel" ./cmd/reaper-tunnel
  GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/reaper-tunnel-agent" ./cmd/reaper-tunnel-agent
  GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/proof-target" ./cmd/proof-target
  GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/proof-websocket" ./cmd/proof-websocket
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
  sed -n '1,120p' "$log" >&2
  exit 1
}

wait_gone() {
  local url=$1
  local label=$2
  local _
  for _ in {1..100}; do
    if ! curl --silent --max-time 1 "$url" > /dev/null 2>&1; then
      return
    fi
    sleep 0.05
  done
  fail "$label kept answering after it was stopped"
}

# wait_exit waits for a process to end on its own and prints its exit status.
wait_exit() {
  local pid=$1
  local label=$2
  local _
  for _ in {1..200}; do
    if ! kill -0 "$pid" 2> /dev/null; then
      break
    fi
    sleep 0.05
  done
  if kill -0 "$pid" 2> /dev/null; then
    fail "$label kept running when it should have stopped"
  fi
  local status=0
  wait "$pid" 2> /dev/null || status=$?
  forget_pid "$pid"
  printf '%s\n' "$status"
}

forget_pid() {
  local pid=$1
  local index
  for index in "${!pids[@]}"; do
    if [[ "${pids[index]}" != "$pid" ]]; then
      continue
    fi
    unset 'pids[index]'
    break
  done
}

boot_server() {
  REAPER_TUNNEL_CONTROL_ADDR="127.0.0.1:$control_port" \
    REAPER_TUNNEL_EDGE_ADDR="127.0.0.1:$edge_port" \
    REAPER_TUNNEL_DOMAIN=$domain \
    REAPER_TUNNEL_DB="$work_dir/tunnel.db" \
    REAPER_TUNNEL_ADMIN_TOKEN=$admin_token \
    REAPER_TUNNEL_ADMIN_ACTOR=$admin_actor \
    REAPER_TUNNEL_READ_TOKEN=$read_token \
    REAPER_TUNNEL_FORWARD_PROTO=http \
    REAPER_TUNNEL_KEEPALIVE=200ms \
    "$work_dir/reaper-tunnel" >> "$work_dir/server.log" 2>&1 &
  server_pid=$!
  pids+=("$server_pid")
  wait_ready "$control_url/healthz" "tunnel server" "$work_dir/server.log"
}

stop_server() {
  if [[ -z "$server_pid" ]]; then
    return
  fi
  terminate_pid "$server_pid"
  forget_pid "$server_pid"
  server_pid=''
  wait_gone "$control_url/healthz" "tunnel server"
}

start_target() {
  local name=$1
  local port=$2
  TARGET_ADDR="127.0.0.1:$port" \
    TARGET_NAME=$name \
    "$work_dir/proof-target" > "$work_dir/target-$name.log" 2>&1 &
  pids+=("$!")
  wait_ready "http://127.0.0.1:$port/health" "$name target" "$work_dir/target-$name.log"
}

# start_agent runs one agent process and records its pid in $work_dir/agent-NAME.pid.
start_agent() {
  local name=$1
  local token=$2
  local target_port=$3
  REAPER_TUNNEL_AGENT_SERVER=$control_url \
    REAPER_TUNNEL_AGENT_TOKEN=$token \
    REAPER_TUNNEL_AGENT_TARGET="http://127.0.0.1:$target_port" \
    REAPER_TUNNEL_AGENT_RECONNECT_DELAYS=100ms,200ms \
    REAPER_TUNNEL_AGENT_KEEPALIVE=200ms \
    "$work_dir/reaper-tunnel-agent" > "$work_dir/agent-$name.log" 2>&1 &
  pids+=("$!")
  printf '%s\n' "$!" > "$work_dir/agent-$name.pid"
}

agent_pid() {
  cat "$work_dir/agent-$1.pid"
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
      "$control_url$path" > "$result"
}

management_status() {
  local path=$1
  local body=$2
  local token=${3-$admin_token}
  local header=(--header "Authorization: Bearer $token")
  if [[ -z "$token" ]]; then
    header=()
  fi
  printf '%s' "$body" |
    curl --silent --output "$work_dir/last-status-body.json" --write-out '%{http_code}' \
      --request POST \
      ${header[@]+"${header[@]}"} \
      --header 'Content-Type: application/json' \
      --data-binary @- \
      "$control_url$path"
}

read_status() {
  local path=$1
  local token=$2
  local header=(--header "Authorization: Bearer $token")
  if [[ -z "$token" ]]; then
    header=()
  fi
  curl --silent --output /dev/null --write-out '%{http_code}' ${header[@]+"${header[@]}"} "$control_url$path"
}

# claim_tunnel issues a credential and prints it; the JSON stays in the work directory.
claim_tunnel() {
  local subdomain=$1
  management_post /v1/tunnels "$(printf '{"subdomain":"%s"}' "$subdomain")" "$work_dir/claim-$subdomain.json"
  jq -e --arg subdomain "$subdomain" '.subdomain == $subdomain and .revision == 1 and (.token | startswith("rtk_"))' \
    "$work_dir/claim-$subdomain.json" > /dev/null || fail "claim for $subdomain was incomplete"
  jq -r '.token' "$work_dir/claim-$subdomain.json"
}

tunnels() {
  curl --fail --silent --show-error --header "Authorization: Bearer $read_token" "$control_url/v1/tunnels"
}

audit() {
  curl --fail --silent --show-error --header "Authorization: Bearer $read_token" "$control_url/v1/audit?limit=${1:-100}"
}

wait_presence() {
  local subdomain=$1
  local presence=$2
  local _
  for _ in {1..200}; do
    if tunnels | jq -e --arg subdomain "$subdomain" --arg presence "$presence" \
      '.tunnels[] | select(.subdomain == $subdomain) | .presence == $presence' > /dev/null 2>&1; then
      return
    fi
    sleep 0.05
  done
  fail "tunnel $subdomain never became $presence"
}

edge_get() {
  local host=$1
  local path=$2
  curl --fail --silent --show-error --header "Host: $host" "$edge_url$path"
}

edge_status() {
  local host=$1
  local path=$2
  curl --silent --output /dev/null --write-out '%{http_code}' --header "Host: $host" "$edge_url$path"
}

wait_edge() {
  local host=$1
  local expected=$2
  local _
  for _ in {1..200}; do
    if [[ "$(edge_status "$host" /health)" == "$expected" ]]; then
      return
    fi
    sleep 0.05
  done
  fail "edge for $host never answered $expected"
}

whoami_field() {
  local host=$1
  local field=$2
  edge_get "$host" "/whoami" | jq -r ".$field"
}
