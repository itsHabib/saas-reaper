#!/usr/bin/env bash
set -euo pipefail

# The credibility claim: a real, unmodified Prometheus Alertmanager container,
# configured with pagerduty_configs exactly as an operator would point it at a
# hosted vendor, opens, deduplicates, and resolves an incident in this specimen.
#
# Alertmanager, the specimen, the responder sink, a real SMTP server, and the
# probe that drives the assertions all sit on one private container network. No
# port is published to the host and nothing leaves the machine.

specimen_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)
cd "$specimen_dir"

alertmanager_image=prom/alertmanager:v0.28.1
mailpit_image=axllent/mailpit:v1.30.1
runtime_image=alpine:3.22

ingest_port=19500
sink_port=19501
smtp_port=19502
mailpit_port=19503
alertmanager_port=19504

admin_token=demo-management-token
read_token=demo-incident-read-token

# The proof shares one directory with its containers. Docker Desktop does not
# share the macOS temp roots, but the checkout is always a shareable path, so the
# work directory lives in the gitignored .reaper/ scratch space instead.
mkdir -p "$specimen_dir/.reaper"
work_dir=$(cd "$(mktemp -d "$specimen_dir/.reaper/demo.XXXXXXXX")" && pwd -P)
network=reaper-incident-demo-$$
containers=()

incidents_host=incidents-$$
sink_host=sink-$$
mail_host=mail-$$
alertmanager_host=alertmanager-$$
probe_host=probe-$$

base_url=http://$incidents_host:$ingest_port
mailpit_url=http://$mail_host:$mailpit_port
alertmanager_url=http://$alertmanager_host:$alertmanager_port

print_logs() {
  local container
  for container in ${containers[@]+"${containers[@]}"}; do
    echo "--- docker logs $container" >&2
    docker logs --tail 60 "$container" 2>&1 | sed -n '1,60p' >&2 || true
  done
}

cleanup() {
  local container
  for container in ${containers[@]+"${containers[@]}"}; do
    docker rm --force "$container" > /dev/null 2>&1 || true
  done
  docker network rm "$network" > /dev/null 2>&1 || true
  if [[ "$work_dir" == */.reaper/demo.* ]]; then
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
  echo "incident demo failed: $1" >&2
  exit 1
}

require_tool() {
  command -v "$1" > /dev/null 2>&1 || fail "$1 is required"
}

# Every HTTP call runs inside the private network from the probe container, so
# the specimen never needs a port published to the host.
probe() {
  docker exec "$probe_host" wget --quiet --output-document - "$@"
}

get_json() {
  probe --header "Authorization: Bearer $read_token" "$1"
}

post_json() {
  probe \
    --header "Authorization: Bearer $admin_token" \
    --header 'Content-Type: application/json' \
    --post-data "$2" \
    "$1"
}

wait_ready() {
  local url=$1
  local label=$2
  local _
  for _ in {1..300}; do
    if probe "$url" > /dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  fail "$label did not become ready at $url"
}

wait_for() {
  local label=$1
  shift
  local _
  for _ in {1..300}; do
    if "$@" > /dev/null 2>&1; then
      return 0
    fi
    sleep 0.2
  done
  fail "$label"
}

require_tool docker
require_tool jq
require_tool go

docker info > /dev/null 2>&1 || fail "the Docker daemon must be running"

arch=$(docker version --format '{{.Server.Arch}}')
echo "building linux/$arch binaries for the proof containers"
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$work_dir/reaper-incidents" ./cmd/reaper-incidents
CGO_ENABLED=0 GOOS=linux GOARCH="$arch" go build -o "$work_dir/responder-sink" ./cmd/responder-sink
chmod 0755 "$work_dir" "$work_dir/reaper-incidents" "$work_dir/responder-sink"

for image in "$alertmanager_image" "$mailpit_image" "$runtime_image"; do
  docker image inspect "$image" > /dev/null 2>&1 || docker pull --quiet "$image" > /dev/null
done

docker network create "$network" > /dev/null

start_container() {
  local name=$1
  shift
  docker run --detach --name "$name" --network "$network" --network-alias "$name" "$@" > /dev/null
  containers+=("$name")
}

echo '{}' > "$work_dir/secrets.json"
chmod 0644 "$work_dir/secrets.json"

echo "starting the probe, a real SMTP server, the responder sink, and the specimen"
start_container "$probe_host" --entrypoint sleep "$runtime_image" infinity

start_container "$mail_host" \
  --env MP_SMTP_BIND_ADDR="0.0.0.0:$smtp_port" \
  --env MP_UI_BIND_ADDR="0.0.0.0:$mailpit_port" \
  "$mailpit_image"

start_container "$sink_host" \
  --volume "$work_dir:/work" \
  --env SINK_ADDR="0.0.0.0:$sink_port" \
  --env SINK_SECRETS=/work/secrets.json \
  --env SINK_RESULT=/work/receipts.jsonl \
  --entrypoint /work/responder-sink \
  "$runtime_image"

start_container "$incidents_host" \
  --volume "$work_dir:/work" \
  --env REAPER_INCIDENT_ADDR="0.0.0.0:$ingest_port" \
  --env REAPER_INCIDENT_DB=/work/incidents.db \
  --env REAPER_INCIDENT_ADMIN_TOKEN="$admin_token" \
  --env REAPER_INCIDENT_ADMIN_ACTOR=demo-operator \
  --env REAPER_INCIDENT_READ_TOKEN="$read_token" \
  --env REAPER_INCIDENT_SMTP_ADDR="$mail_host:$smtp_port" \
  --env REAPER_INCIDENT_SMTP_FROM=pager@reaper.invalid \
  --env REAPER_INCIDENT_POLL_INTERVAL=100ms \
  --env REAPER_INCIDENT_NOTIFY_RETRY_DELAYS=1s,2s \
  --entrypoint /work/reaper-incidents \
  "$runtime_image"

wait_ready "$base_url/healthz" "the incident specimen"
wait_ready "$mailpit_url/api/v1/info" "the SMTP server"
wait_ready "http://$sink_host:$sink_port/healthz" "the responder sink"

echo "registering responders, the on-call schedule, the ladder, and the service"
post_json "$base_url/v1/responders" \
  "{\"id\":\"ada\",\"email\":\"ada@example.test\",\"webhookUrl\":\"http://$sink_host:$sink_port/page/ada\"}" \
  > "$work_dir/ada.json"
post_json "$base_url/v1/responders" '{"id":"grace","email":"grace@example.test"}' > /dev/null
post_json "$base_url/v1/responders" '{"id":"linus","email":"linus@example.test"}' > /dev/null

jq -n --arg secret "$(jq -er '.webhookSecret' "$work_dir/ada.json")" '{ada: $secret}' > "$work_dir/secrets.json"

post_json "$base_url/v1/schedules" "$(cat fixtures/schedule.json)" > /dev/null
post_json "$base_url/v1/escalation-policies" "$(cat fixtures/escalation-policy.json)" > /dev/null
post_json "$base_url/v1/services" \
  '{"id":"payments","name":"Payments API","escalationPolicy":"payments-ladder"}' \
  > "$work_dir/service.json"
routing_key=$(jq -er '.routingKey' "$work_dir/service.json")
[[ -n "$routing_key" ]] || fail "the service did not mint a routing key"

echo "starting an unmodified $alertmanager_image against the specimen's Events API v2 endpoint"
sed \
  -e "s|ROUTING_KEY|$routing_key|" \
  -e "s|INGEST_URL|$base_url/v2/enqueue|" \
  fixtures/alertmanager.yml.template > "$work_dir/alertmanager.yml"
chmod 0644 "$work_dir/alertmanager.yml"

start_container "$alertmanager_host" \
  --volume "$work_dir/alertmanager.yml:/etc/alertmanager/alertmanager.yml:ro" \
  "$alertmanager_image" \
  --config.file=/etc/alertmanager/alertmanager.yml \
  --storage.path=/tmp/alertmanager \
  --web.listen-address=":$alertmanager_port"
wait_ready "$alertmanager_url/-/ready" "Alertmanager"

alertmanager_version=$(probe "$alertmanager_url/api/v2/status" | jq -er '.versionInfo.version')
echo "Alertmanager $alertmanager_version is ready and pointed at $base_url/v2/enqueue"

incident_count() {
  get_json "$base_url/v1/incidents" | jq -r '.incidents | length'
}

# A firing alert carries no endsAt, so Alertmanager holds it open for the
# configured resolve_timeout. Sending the same alert with endsAt in the past is
# how a real Prometheus reports that the condition cleared.
post_alert() {
  local payload=$1
  probe --header 'Content-Type: application/json' --post-data "$payload" \
    "$alertmanager_url/api/v2/alerts" > /dev/null
}

alert_payload() {
  jq -c -n --arg starts "$1" --arg ends "$2" '[{
    labels: {alertname: "PaymentsDown", severity: "critical", service: "payments"},
    annotations: {summary: "payments API is returning 5xx", description: "error budget exhausted"},
    startsAt: $starts
  } + (if $ends == "" then {} else {endsAt: $ends} end)]'
}

fire_alert() {
  post_alert "$(alert_payload "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "")"
}

resolve_alert() {
  post_alert "$(alert_payload "$(date -u +%Y-%m-%dT%H:%M:%SZ)" "$(date -u +%Y-%m-%dT%H:%M:%SZ)")"
}

echo "firing an alert through Alertmanager's own API"
fire_alert

wait_for "Alertmanager never opened an incident in the specimen" \
  bash -c "test \"\$(docker exec $probe_host wget -q -O - --header 'Authorization: Bearer $read_token' '$base_url/v1/incidents' | jq -r '.incidents | length')\" = 1"

get_json "$base_url/v1/incidents" > "$work_dir/incidents.json"
jq -e '.incidents | length == 1' "$work_dir/incidents.json" > /dev/null ||
  fail "expected exactly one incident from one Alertmanager group"

incident_id=$(jq -er '.incidents[0].id' "$work_dir/incidents.json")
dedup_key=$(jq -er '.incidents[0].dedupKey' "$work_dir/incidents.json")

jq -e '
  .incidents[0]
  | .state == "triggered"
    and .severity == "critical"
    and .client == "Alertmanager"
    and (.summary | length > 0)
    and (.source | length > 0)
    and .escalationPolicy == "payments-ladder"
    and .level == 0
' "$work_dir/incidents.json" > /dev/null ||
  fail "the opened incident does not carry Alertmanager's event payload"

# Alertmanager derives dedup_key as the hex SHA-256 of its aggregation group key.
[[ "$dedup_key" =~ ^[0-9a-f]{64}$ ]] ||
  fail "dedup_key is not Alertmanager's hashed group key: $dedup_key"

echo "incident $incident_id opened with Alertmanager's dedup key ${dedup_key:0:16}..."

echo "re-firing the same alert to prove one dedup key opens exactly one incident"
fire_alert
sleep 3
[[ "$(incident_count)" == 1 ]] || fail "a repeated Alertmanager notification opened a second incident"

get_json "$base_url/v1/incidents/$incident_id/events" > "$work_dir/events.json"
jq -e '[.events[] | select(.kind == "opened")] | length == 1' "$work_dir/events.json" > /dev/null ||
  fail "the journal does not record exactly one opening"

echo "checking the signed page reached the responder and the official verifier accepted it"
wait_for "the responder sink never recorded a verified page" \
  bash -c "test -s '$work_dir/receipts.jsonl'"
jq -s -e '
  length >= 1
    and (.[0].responder == "ada")
    and (.[0].accepted == true)
    and (.[0].tamperedRejected == true)
    and (.[0].payload.incident.state == "triggered")
' "$work_dir/receipts.jsonl" > /dev/null ||
  fail "the official Standard Webhooks verifier did not accept the page"

echo "checking the real SMTP server received the email page"
wait_for "no email page arrived at the SMTP server" \
  bash -c "test \"\$(docker exec $probe_host wget -q -O - '$mailpit_url/api/v1/messages' | jq -r '.messages | length')\" -ge 1"
probe "$mailpit_url/api/v1/messages" > "$work_dir/mail.json"
jq -e '
  .messages[0]
  | (.To[0].Address == "ada@example.test")
    and (.From.Address == "pager@reaper.invalid")
    and (.Subject | startswith("[CRITICAL] Payments API:"))
' "$work_dir/mail.json" > /dev/null || fail "the email page does not carry the incident context"

echo "checking every notification attempt is audited exactly once"
get_json "$base_url/v1/attempts?incidentId=$incident_id" > "$work_dir/attempts.json"
jq -e '
  (.attempts | length) >= 2
    and ([.attempts[] | select(.outcome == "delivered")] | length == 2)
    and ([.attempts[] | "\(.notificationId)/\(.number)"] | (length == (unique | length)))
' "$work_dir/attempts.json" > /dev/null ||
  fail "the append-only audit does not hold one row per attempt"

echo "resolving the alert in Alertmanager"
resolve_alert
wait_for "Alertmanager's resolve never closed the incident" \
  bash -c "test \"\$(docker exec $probe_host wget -q -O - --header 'Authorization: Bearer $read_token' '$base_url/v1/incidents/$incident_id' | jq -r '.state')\" = resolved"

get_json "$base_url/v1/incidents/$incident_id" > "$work_dir/resolved.json"
jq -e --arg key "$dedup_key" '
  .state == "resolved" and .dedupKey == $key and (has("escalateAt") | not)
' "$work_dir/resolved.json" > /dev/null ||
  fail "the resolve did not close the same incident identity"

get_json "$base_url/v1/incidents/$incident_id/events" > "$work_dir/events-after.json"
jq -e '[.events[] | select(.kind == "resolved")] | length == 1' "$work_dir/events-after.json" > /dev/null ||
  fail "the journal does not record the resolve"

echo
echo "incident escalation demo: unmodified Alertmanager $alertmanager_version opened, deduplicated,"
echo "paged (signed webhook verified by the official Standard Webhooks library, plus real SMTP),"
echo "and resolved incident $incident_id through the Events API v2 contract"
