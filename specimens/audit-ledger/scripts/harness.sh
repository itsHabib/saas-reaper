#!/usr/bin/env bash
# Shared lifecycle for the audit ledger's local proofs. Sourced, not executed.
# Callers set service_port, write_token, read_token, and read_tenants first.

set -euo pipefail

: "${service_port:?harness requires service_port}"
: "${write_token:?harness requires write_token}"
: "${write_principal:?harness requires write_principal}"
: "${read_token:?harness requires read_token}"
: "${read_tenants:?harness requires read_tenants}"

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
work_dir=$(mktemp -d)
database_path=$work_dir/audit.db
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
  # bash 3.2 aborts on an empty array under set -u; the expansion guard keeps
  # the temporary directory removal reachable on every failure path.
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
  sed -n '1,160p' "$log" >&2
  exit 1
}

build_service() {
  cd "$specimen_dir"
  GOTOOLCHAIN=local GOPROXY=off go build -o "$work_dir/reaper-audit" ./cmd/reaper-audit
  base_url=http://127.0.0.1:$service_port
}

boot_service() {
  REAPER_AUDIT_ADDR="127.0.0.1:$service_port" \
    REAPER_AUDIT_DB=$database_path \
    REAPER_AUDIT_WRITE_TOKEN=$write_token \
    REAPER_AUDIT_WRITE_PRINCIPAL=$write_principal \
    REAPER_AUDIT_READ_TOKEN=$read_token \
    REAPER_AUDIT_READ_TENANTS=$read_tenants \
    "$work_dir/reaper-audit" >> "$work_dir/server.log" 2>&1 &
  server_pid=$!
  pids+=("$server_pid")
  wait_ready "$base_url/healthz" "audit service" "$work_dir/server.log"
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

response_status() {
  curl --silent --output /dev/null --write-out '%{http_code}' "$@"
}

ingest() {
  local body=$1
  local result=$2
  curl --silent --show-error --output "$result" --write-out '%{http_code}' \
    --request POST \
    --header "Authorization: Bearer $write_token" \
    --header 'Content-Type: application/json' \
    --data-binary "$body" \
    "$base_url/v1/events"
}

read_head() {
  local tenant=$1
  curl --fail --silent --show-error \
    --header "Authorization: Bearer $read_token" \
    "$base_url/v1/tenants/$tenant/head"
}

read_export() {
  local tenant=$1
  local result=$2
  curl --fail --silent --show-error \
    --header "Authorization: Bearer $read_token" \
    "$base_url/v1/tenants/$tenant/export" > "$result"
}

verify_export() {
  python3 "$specimen_dir/verifier/verify.py" "$1"
}
