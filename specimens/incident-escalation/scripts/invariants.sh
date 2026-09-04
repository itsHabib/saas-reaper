#!/usr/bin/env bash
set -euo pipefail

# Black-box probes for the invariants the pager lives or dies by. Everything runs
# as host processes on 127.0.0.1 with an injected clock offset, so no probe waits
# on wall-clock escalation timeouts and no container runtime is required.

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"

ingest_port=19505
sink_port=19506

admin_token=invariants-management-token
read_token=invariants-incident-read-token

work_dir=$(mktemp -d)
database_path=$work_dir/incidents.db
base_url=http://127.0.0.1:$ingest_port
server_pid=''
sink_pid=''
pids=()
clock_offset=''

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
  echo "incident specimen broke an invariant: $1" >&2
  exit 1
}

management() {
  curl --fail --silent --show-error \
    --header "Authorization: Bearer $admin_token" \
    --header 'Content-Type: application/json' \
    "$@"
}

read_api() {
  curl --fail --silent --show-error \
    --header "Authorization: Bearer $read_token" \
    "$@"
}

status_code() {
  curl --silent --output /dev/null --write-out '%{http_code}' "$@"
}

boot_service() {
  (
    export REAPER_INCIDENT_ADDR=127.0.0.1:$ingest_port
    export REAPER_INCIDENT_DB="$database_path"
    export REAPER_INCIDENT_ADMIN_TOKEN="$admin_token"
    export REAPER_INCIDENT_ADMIN_ACTOR=invariants
    export REAPER_INCIDENT_READ_TOKEN="$read_token"
    export REAPER_INCIDENT_POLL_INTERVAL=100ms
    export REAPER_INCIDENT_NOTIFY_RETRY_DELAYS=1s,2s
    if [[ -n "$clock_offset" ]]; then
      export REAPER_INCIDENT_CLOCK_OFFSET="$clock_offset"
    fi
    exec "$work_dir/reaper-incidents"
  ) >> "$work_dir/service.log" 2>&1 &
  server_pid=$!
  pids+=("$server_pid")
  local _
  for _ in {1..300}; do
    if curl --fail --silent "$base_url/healthz" > /dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  fail "the service did not become ready"
}

stop_service() {
  terminate_pid "$server_pid"
  local remaining=()
  local pid
  for pid in ${pids[@]+"${pids[@]}"}; do
    if [[ "$pid" != "$server_pid" ]]; then
      remaining+=("$pid")
    fi
  done
  pids=(${remaining[@]+"${remaining[@]}"})
  server_pid=''
}

wait_for() {
  local label=$1
  shift
  local _
  for _ in {1..300}; do
    if "$@" > /dev/null 2>&1; then
      return 0
    fi
    sleep 0.1
  done
  fail "$label"
}

incident_state() {
  read_api "$base_url/v1/incidents/$1" | jq -r '.state'
}

incident_level() {
  read_api "$base_url/v1/incidents/$1" | jq -r '.level'
}

trigger() {
  local key=$1
  local summary=${2:-payments API is down}
  curl --fail --silent --show-error \
    --header 'Content-Type: application/json' \
    --request POST \
    --data "{\"routing_key\":\"$routing_key\",\"event_action\":\"trigger\",\"dedup_key\":\"$key\",\"client\":\"probe\",\"payload\":{\"summary\":\"$summary\",\"source\":\"probe\",\"severity\":\"critical\"}}" \
    "$base_url/v2/enqueue"
}

wire_action() {
  local action=$1
  local key=$2
  curl --fail --silent --show-error \
    --header 'Content-Type: application/json' \
    --request POST \
    --data "{\"routing_key\":\"$routing_key\",\"event_action\":\"$action\",\"dedup_key\":\"$key\"}" \
    "$base_url/v2/enqueue"
}

command -v jq > /dev/null 2>&1 || fail "jq is required"
command -v curl > /dev/null 2>&1 || fail "curl is required"

go build -o "$work_dir/reaper-incidents" ./cmd/reaper-incidents
go build -o "$work_dir/responder-sink" ./cmd/responder-sink

echo '{}' > "$work_dir/secrets.json"
(
  export SINK_ADDR=127.0.0.1:$sink_port
  export SINK_SECRETS="$work_dir/secrets.json"
  export SINK_RESULT="$work_dir/receipts.jsonl"
  exec "$work_dir/responder-sink"
) >> "$work_dir/sink.log" 2>&1 &
sink_pid=$!
pids+=("$sink_pid")
wait_for "the responder sink did not become ready" \
  curl --fail --silent "http://127.0.0.1:$sink_port/healthz"

boot_service

# ---------------------------------------------------------------------------
# Catalog: two responders, one schedule with an override, a two-level ladder.
# ---------------------------------------------------------------------------
management --request POST \
  --data "{\"id\":\"ada\",\"email\":\"ada@example.test\",\"webhookUrl\":\"http://127.0.0.1:$sink_port/page/ada\"}" \
  "$base_url/v1/responders" > "$work_dir/ada.json"
management --request POST \
  --data "{\"id\":\"linus\",\"webhookUrl\":\"http://127.0.0.1:$sink_port/page/linus\"}" \
  "$base_url/v1/responders" > "$work_dir/linus.json"
management --request POST --data '{"id":"grace","email":"grace@example.test"}' \
  "$base_url/v1/responders" > /dev/null

jq -n \
  --arg ada "$(jq -er '.webhookSecret' "$work_dir/ada.json")" \
  --arg linus "$(jq -er '.webhookSecret' "$work_dir/linus.json")" \
  '{ada: $ada, linus: $linus}' > "$work_dir/secrets.json"

management --request POST --data @fixtures/schedule.json "$base_url/v1/schedules" > /dev/null
management --request POST --data @fixtures/escalation-policy.json "$base_url/v1/escalation-policies" > /dev/null
management --request POST \
  --data '{"id":"payments","name":"Payments API","escalationPolicy":"payments-ladder"}' \
  "$base_url/v1/services" > "$work_dir/service.json"
routing_key=$(jq -er '.routingKey' "$work_dir/service.json")

# ---------------------------------------------------------------------------
# 1. One dedup key opens exactly one incident.
# ---------------------------------------------------------------------------
trigger dedup-one > /dev/null
trigger dedup-one > /dev/null
trigger dedup-one > /dev/null
read_api "$base_url/v1/incidents?serviceId=payments" > "$work_dir/dedup.json"
jq -e '.incidents | length == 1' "$work_dir/dedup.json" > /dev/null ||
  fail "three triggers with one dedup key opened more than one incident"
dedup_incident=$(jq -er '.incidents[0].id' "$work_dir/dedup.json")
read_api "$base_url/v1/incidents/$dedup_incident/events" > "$work_dir/dedup-events.json"
jq -e '
  ([.events[] | select(.kind == "opened")] | length == 1)
    and ([.events[] | select(.kind == "retriggered")] | length == 2)
' "$work_dir/dedup-events.json" > /dev/null ||
  fail "duplicate triggers were not journaled against the one open incident"
echo "dedup invariant: one dedup key holds one open incident and journals repeats"

# ---------------------------------------------------------------------------
# 2. The schedule override wins over the rotation at the overridden time.
# ---------------------------------------------------------------------------
rotation_holder=$(read_api "$base_url/v1/schedules/payments-primary/on-call?at=2026-01-06T00:00:00Z" | jq -r '.responder')
override_holder=$(read_api "$base_url/v1/schedules/payments-primary/on-call?at=2026-01-11T00:00:00Z" | jq -r '.responder')
after_override=$(read_api "$base_url/v1/schedules/payments-primary/on-call?at=2026-01-12T10:00:00Z" | jq -r '.responder')
[[ "$rotation_holder" == "ada" ]] || fail "the first rotation slot is not ada: $rotation_holder"
[[ "$override_holder" == "linus" ]] || fail "the override did not win: $override_holder"
[[ "$after_override" == "grace" ]] || fail "the rotation did not resume after the override: $after_override"
echo "schedule invariant: an override wins inside its window and the rotation resumes after it"

# ---------------------------------------------------------------------------
# 3. An unacknowledged incident escalates to level 2 exactly at the timeout and
#    pages the responder that level names.
# ---------------------------------------------------------------------------
trigger escalate-me > /dev/null
escalate_incident=$(read_api "$base_url/v1/incidents?serviceId=payments" |
  jq -er '[.incidents[] | select(.dedupKey == "escalate-me")][0].id')
wait_for "the opening page never reached ada" \
  bash -c "test \"\$(grep -c '\"responder\":\"ada\"' '$work_dir/receipts.jsonl' 2> /dev/null || echo 0)\" -ge 1"
[[ "$(incident_level "$escalate_incident")" == 0 ]] || fail "a new incident did not start at level 0"
sleep 1
[[ "$(incident_level "$escalate_incident")" == 0 ]] ||
  fail "the incident escalated before its 30s level-0 timeout"

# Advance the injected clock past the level-0 timeout by restarting on the same
# database; the timer lives in the row, so nothing is lost across the restart.
stop_service
clock_offset=45s
boot_service
wait_for "the level-0 timeout did not escalate the incident" \
  bash -c "test \"\$(curl --fail --silent --header 'Authorization: Bearer $read_token' '$base_url/v1/incidents/$escalate_incident' | jq -r '.level')\" = 1"
wait_for "level 1 never paged linus" \
  bash -c "test \"\$(grep -c '\"responder\":\"linus\"' '$work_dir/receipts.jsonl' 2> /dev/null || echo 0)\" -ge 1"
read_api "$base_url/v1/incidents/$escalate_incident/events" > "$work_dir/escalation-events.json"
jq -e '[.events[] | select(.kind == "escalated")] | length == 1' "$work_dir/escalation-events.json" > /dev/null ||
  fail "the escalation was not journaled exactly once"
jq -e '[.events[] | select(.kind == "escalated")][0].actor == "system:escalation-timer"' \
  "$work_dir/escalation-events.json" > /dev/null ||
  fail "the escalation was not attributed to the durable timer"
echo "escalation invariant: an unacknowledged incident climbs one level at its timeout and pages that level"
echo "durability invariant: the escalation timer survived a process restart on the same database"

# ---------------------------------------------------------------------------
# 4. Acknowledging stops escalation for good.
# ---------------------------------------------------------------------------
trigger ack-me > /dev/null
ack_incident=$(read_api "$base_url/v1/incidents?serviceId=payments" |
  jq -er '[.incidents[] | select(.dedupKey == "ack-me")][0].id')
wire_action acknowledge ack-me > /dev/null
[[ "$(incident_state "$ack_incident")" == acknowledged ]] || fail "the wire acknowledge did not take ownership"
read_api "$base_url/v1/incidents/$ack_incident" > "$work_dir/acked.json"
jq -e 'has("escalateAt") | not' "$work_dir/acked.json" > /dev/null ||
  fail "an acknowledged incident still holds an armed timer"

stop_service
clock_offset=10m
boot_service
sleep 1
[[ "$(incident_level "$ack_incident")" == 0 ]] ||
  fail "an acknowledged incident escalated after the clock advanced"
[[ "$(incident_state "$ack_incident")" == acknowledged ]] ||
  fail "an acknowledged incident changed state after the clock advanced"
echo "acknowledge invariant: ownership stops escalation even ten minutes past the timeout"

# ---------------------------------------------------------------------------
# 5. A resolve from ingest closes the incident and disarms its timer.
# ---------------------------------------------------------------------------
wire_action resolve dedup-one > /dev/null
[[ "$(incident_state "$dedup_incident")" == resolved ]] || fail "the wire resolve did not close the incident"
read_api "$base_url/v1/incidents/$dedup_incident" > "$work_dir/resolved.json"
jq -e 'has("escalateAt") | not' "$work_dir/resolved.json" > /dev/null ||
  fail "a resolved incident still holds an armed timer"
trigger dedup-one > /dev/null
read_api "$base_url/v1/incidents?serviceId=payments" > "$work_dir/after-resolve.json"
jq -e '[.incidents[] | select(.dedupKey == "dedup-one")] | length == 2' "$work_dir/after-resolve.json" > /dev/null ||
  fail "a trigger after resolve did not open a fresh incident"
echo "resolve invariant: ingest closes the incident and a later trigger opens a new identity"

# ---------------------------------------------------------------------------
# 6. Token separation in both directions.
# ---------------------------------------------------------------------------
[[ "$(status_code --header "Authorization: Bearer $admin_token" "$base_url/v1/incidents")" == 401 ]] ||
  fail "the management token could read incidents"
[[ "$(status_code --header "Authorization: Bearer $admin_token" "$base_url/v1/attempts")" == 401 ]] ||
  fail "the management token could read the notification audit"
[[ "$(status_code --request POST --header "Authorization: Bearer $read_token" \
  --header 'Content-Type: application/json' --data '{"id":"mallory","email":"m@example.test"}' \
  "$base_url/v1/responders")" == 401 ]] ||
  fail "the audit-read token could register a responder"
[[ "$(status_code --request POST --header "Authorization: Bearer $read_token" \
  "$base_url/v1/incidents/$ack_incident/resolve")" == 401 ]] ||
  fail "the audit-read token could resolve an incident"
[[ "$(status_code "$base_url/v1/incidents")" == 401 ]] ||
  fail "an unauthenticated caller could read incidents"
echo "authority invariant: management and audit-read tokens cannot borrow each other's power"

# ---------------------------------------------------------------------------
# 7. The audit holds exactly one row per notification attempt, and never leaks
#    a signing secret or a destination.
# ---------------------------------------------------------------------------
read_api "$base_url/v1/attempts?limit=1000" > "$work_dir/attempts.json"
jq -e '
  (.attempts | length) >= 3
    and ([.attempts[] | "\(.notificationId)/\(.number)"] | (length == (unique | length)))
    and (all(.attempts[]; .actor == null))
' "$work_dir/attempts.json" > /dev/null ||
  fail "the append-only audit does not hold exactly one row per attempt"
if grep -q 'whsec_' "$work_dir/attempts.json"; then
  fail "the audit read exposed a responder signing secret"
fi
if grep -q '127.0.0.1' "$work_dir/attempts.json"; then
  fail "the audit read exposed a responder destination"
fi
delivered=$(jq -r '[.attempts[] | select(.outcome == "delivered")] | length' "$work_dir/attempts.json")
[[ "$delivered" -ge 3 ]] || fail "expected at least three delivered pages, got $delivered"
echo "audit invariant: one append-only row per attempt with no secret or destination exposed"

# ---------------------------------------------------------------------------
# 8. The append-only journal and audit survive the restarts above unchanged.
# ---------------------------------------------------------------------------
jq -cS '[.attempts[] | {sequence, notificationId, number, outcome, attemptedAt}]' \
  "$work_dir/attempts.json" > "$work_dir/audit-before.json"
stop_service
boot_service
read_api "$base_url/v1/attempts?limit=1000" > "$work_dir/attempts-after.json"
jq -cS '[.attempts[] | {sequence, notificationId, number, outcome, attemptedAt}]' \
  "$work_dir/attempts-after.json" > "$work_dir/audit-after.json"
# Append-only means every earlier row survives byte-identical. New rows may still
# arrive, because pages queued before the restart are legitimately delivered after it.
jq -e -n \
  --slurpfile before "$work_dir/audit-before.json" \
  --slurpfile after "$work_dir/audit-after.json" \
  '($before[0] - $after[0]) == [] and ($after[0] | length) >= ($before[0] | length)' > /dev/null ||
  fail "an audit row was mutated or removed across a restart"
echo "append-only invariant: every audit row written before a restart survives it unchanged"

echo
echo "incident invariants: dedup, schedule overrides, timed escalation, restart-durable timers,"
echo "acknowledge, resolve, token separation, and exactly-once attempt auditing all hold"
