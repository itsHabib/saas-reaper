#!/usr/bin/env bash
set -euo pipefail

proof_label='notification invariant'
admin_actor=invariants
harness_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/harness.sh
source "$harness_dir/harness.sh"

trap finish EXIT
trap 'exit 130' INT TERM

idempotency_port=19402
preference_port=19403
retry_port=19404

require_free_ports "$service_port" "$smtp_port" "$idempotency_port" "$preference_port" "$retry_port"
build_binaries

start_smtp_sink restart "$smtp_port" "$work_dir/restart-receipts.jsonl" 1
start_slack_sink idempotency "$idempotency_port" "$work_dir/idempotency-receipts.jsonl" 0
start_slack_sink preference "$preference_port" "$work_dir/preference-receipts.jsonl" 0
start_slack_sink retry "$retry_port" "$work_dir/retry-receipts.jsonl" 2
boot_service

# Token separation, both directions.
read_with_management=$(response_status \
  --header "Authorization: Bearer $admin_token" \
  "$base_url/v1/attempts")
[[ "$read_with_management" == 401 ]] ||
  fail "management token read the attempt audit with status $read_with_management, want 401"

send_with_read=$(response_status \
  --request POST \
  --header "Authorization: Bearer $read_token" \
  --header 'Content-Type: application/json' \
  --data-binary '{"template":"invoice-paid","recipient":"cus_idem","idempotencyKey":"k","payload":{}}' \
  "$base_url/v1/notifications")
[[ "$send_with_read" == 401 ]] ||
  fail "audit-read token sent a notification with status $send_with_read, want 401"

channel_with_read=$(response_status \
  --request POST \
  --header "Authorization: Bearer $read_token" \
  --header 'Content-Type: application/json' \
  --data-binary '{"id":"sneaky","kind":"smtp"}' \
  "$base_url/v1/channels")
[[ "$channel_with_read" == 401 ]] ||
  fail "audit-read token registered a channel with status $channel_with_read, want 401"

read_with_read=$(response_status \
  --header "Authorization: Bearer $read_token" \
  "$base_url/v1/attempts")
[[ "$read_with_read" == 200 ]] ||
  fail "audit-read token could not read the attempt audit: status $read_with_read"
echo "token separation: management cannot read the audit and the read authority cannot send or manage"

register_channel email smtp
register_channel chat slack-webhook
register_channel muted slack-webhook
create_template invoice-paid email 'Invoice {{invoice.id}}' 'Paid {{invoice.amount}} {{invoice.currency}}'
create_template invoice-paid chat '' '{{customer.name}} paid {{invoice.id}}'
create_template invoice-paid muted '' '{{customer.name}} paid {{invoice.id}}'
disable_channel muted

create_recipient cus_idem "$(jq -cn --arg webhook "http://127.0.0.1:$idempotency_port/services/T0/B0/idem" '{
  id: "cus_idem",
  channels: [{channel: "chat", address: $webhook}]
}')"

# A template variable missing from the payload is rejected at send time, before anything is queued.
missing_status=$(send_status invariant-missing cus_idem '{"invoice":{"id":"inv_1"}}')
[[ "$missing_status" == 400 ]] ||
  fail "a send missing a template variable returned $missing_status, want 400"
grep -q 'is missing from payload' "$work_dir/send-status-body.json" ||
  fail "the rejection did not name the missing template variable"
sleep 0.3
[[ ! -s "$work_dir/idempotency-receipts.jsonl" ]] ||
  fail "a send rejected at render time still reached a transport"
echo "render rejection invariant: a missing template variable is refused at send time, not at delivery time"

# The same idempotency key delivers exactly once.
send_notification invariant-idempotent cus_idem "$work_dir/idempotent-first.json"
idempotent_id=$(jq -er '.notificationId' "$work_dir/idempotent-first.json")
jq -e '.deduplicated == false and (.deliveries | length == 1)' "$work_dir/idempotent-first.json" > /dev/null ||
  fail "the first send did not queue exactly one delivery"
wait_receipts "$work_dir/idempotency-receipts.jsonl" 1 "idempotency receiver"
wait_attempts "$idempotent_id" 1 "$work_dir/idempotent-attempts.json"

send_notification invariant-idempotent cus_idem "$work_dir/idempotent-second.json"
jq -e --arg notification "$idempotent_id" \
  --slurpfile first "$work_dir/idempotent-first.json" '
  .notificationId == $notification
    and .deduplicated == true
    and ([.deliveries[].id] == [$first[0].deliveries[].id])
' "$work_dir/idempotent-second.json" > /dev/null ||
  fail "the repeated idempotency key did not return the first acceptance"

conflict_status=$(send_status invariant-idempotent cus_idem '{"customer":{"name":"Other"},"invoice":{"id":"inv_9"}}')
[[ "$conflict_status" == 409 ]] ||
  fail "reusing an idempotency key for a different payload returned $conflict_status, want 409"

sleep 0.4
jq -s -e 'length == 1' "$work_dir/idempotency-receipts.jsonl" > /dev/null ||
  fail "the deduplicated re-send delivered a second time"
attempts_for "$idempotent_id" > "$work_dir/idempotent-attempts.json"
jq -e '(.attempts | length == 1) and .attempts[0].outcome == "delivered"' \
  "$work_dir/idempotent-attempts.json" > /dev/null ||
  fail "the deduplicated re-send produced a second attempt row"
echo "idempotency invariant: a repeated key returned the first acceptance and delivered exactly once"

# A disabled preference and a disabled channel are both honored.
create_recipient cus_pref "$(jq -cn \
  --arg muted "http://127.0.0.1:$preference_port/services/T0/B0/muted" \
  --arg quiet "http://127.0.0.1:$preference_port/services/T0/B0/quiet" '{
  id: "cus_pref",
  channels: [
    {channel: "chat", address: $quiet, enabled: false},
    {channel: "muted", address: $muted, enabled: true}
  ]
}')"
send_notification invariant-preference cus_pref "$work_dir/preference-send.json"
preference_id=$(jq -er '.notificationId' "$work_dir/preference-send.json")
jq -e '.deliveries | length == 0' "$work_dir/preference-send.json" > /dev/null ||
  fail "a disabled preference or disabled channel still queued a delivery"
sleep 0.4
[[ ! -s "$work_dir/preference-receipts.jsonl" ]] ||
  fail "a suppressed channel received an HTTP request"
attempts_for "$preference_id" > "$work_dir/preference-attempts.json"
jq -e '.attempts | length == 0' "$work_dir/preference-attempts.json" > /dev/null ||
  fail "a suppressed channel produced an attempt row"
echo "preference invariant: a recipient-disabled binding and an operator-disabled channel both stayed silent"

# A transient transport failure retries, and every try is audited exactly once.
create_recipient cus_retry "$(jq -cn --arg webhook "http://127.0.0.1:$retry_port/services/T0/B0/retry" '{
  id: "cus_retry",
  channels: [{channel: "chat", address: $webhook}]
}')"
send_notification invariant-retry cus_retry "$work_dir/retry-send.json"
retry_id=$(jq -er '.notificationId' "$work_dir/retry-send.json")
wait_receipts "$work_dir/retry-receipts.jsonl" 3 "retry receiver"
wait_attempts "$retry_id" 3 "$work_dir/retry-attempts.json"
jq -s -e '
  length == 3
    and [.[].sequence] == [1, 2, 3]
    and [.[].attempt] == ["1", "2", "3"]
    and (all(.[]; .valid == true))
    and [.[].rejected] == [true, true, false]
' "$work_dir/retry-receipts.jsonl" > /dev/null ||
  fail "the retry receiver did not see three identical, valid attempts"
jq -e '
  ([.attempts[]] | sort_by(.number)) as $rows
    | ($rows | length == 3)
      and [$rows[].number] == [1, 2, 3]
      and [$rows[].outcome] == ["retrying", "retrying", "delivered"]
      and [$rows[].state] == ["pending", "pending", "delivered"]
      and [$rows[].code] == [503, 503, 200]
      and ($rows[0].nextAttemptAt != null)
      and ($rows[1].nextAttemptAt != null)
      and ($rows[2] | has("nextAttemptAt") | not)
      and ($rows[0].sequence < $rows[1].sequence)
      and ($rows[1].sequence < $rows[2].sequence)
' "$work_dir/retry-attempts.json" > /dev/null ||
  fail "the two transport failures were not audited exactly once each before the retry succeeded"
echo "retry invariant: two 5xx replies were audited once per try before delivery succeeded"

# Pending work and the append-only audit survive a restart on the same database.
stop_service
retry_delays=3s,100ms
boot_service
create_recipient cus_restart '{"id":"cus_restart","channels":[{"channel":"email","address":"restart@acme.example"}]}'
send_notification invariant-restart cus_restart "$work_dir/restart-send.json"
restart_id=$(jq -er '.notificationId' "$work_dir/restart-send.json")
jq -e '.deliveries | length == 1' "$work_dir/restart-send.json" > /dev/null ||
  fail "the restart publication did not queue one delivery"
wait_receipts "$work_dir/restart-receipts.jsonl" 1 "restart SMTP sink"
wait_attempts "$restart_id" 1 "$work_dir/restart-before.json"
jq -e '
  (.attempts | length == 1)
    and .attempts[0].number == 1
    and .attempts[0].outcome == "retrying"
    and .attempts[0].state == "pending"
    and .attempts[0].code == 451
    and (.attempts[0].nextAttemptAt != null)
' "$work_dir/restart-before.json" > /dev/null ||
  fail "the restart setup did not persist one pending retry and its audit"
restart_first_before=$(jq -cS '.attempts[0]' "$work_dir/restart-before.json")

stop_service
boot_service
wait_receipts "$work_dir/restart-receipts.jsonl" 2 "restart SMTP sink"
wait_attempts "$restart_id" 2 "$work_dir/restart-after.json"
restart_first_after=$(jq -cS '.attempts[] | select(.number == 1)' "$work_dir/restart-after.json")
[[ "$restart_first_after" == "$restart_first_before" ]] ||
  fail "the first attempt audit changed across the restart"
jq -e '
  ([.attempts[]] | sort_by(.number)) as $rows
    | ($rows | length == 2)
      and [$rows[].number] == [1, 2]
      and [$rows[].outcome] == ["retrying", "delivered"]
      and [$rows[].code] == [451, 250]
      and ($rows[0].sequence < $rows[1].sequence)
' "$work_dir/restart-after.json" > /dev/null ||
  fail "the pending delivery did not resume successfully after the restart"
jq -s -e '
  length == 2
    and [.[].rejected] == [true, false]
    and (all(.[]; .subject == "Invoice inv_20260901_001" and .body == "Paid 4200.50 usd"))
    and ([.[].messageId] | unique | length == 1)
' "$work_dir/restart-receipts.jsonl" > /dev/null ||
  fail "the resumed attempt changed the rendered message or its identity"
echo "restart invariant: pending work and the append-only audit survived a restart on the same SQLite authority"

curl --fail --silent --show-error \
  --header "Authorization: Bearer $read_token" \
  "$base_url/v1/attempts?limit=1000" > "$work_dir/all-attempts.json"
jq -e '
  (.attempts | length == 6)
    and (all(.attempts[]; .actor == "invariants"))
    and ([.attempts[].sequence] | unique | length == 6)
' "$work_dir/all-attempts.json" > /dev/null ||
  fail "the append-only audit lost, duplicated, or reattributed rows across the run"

echo "notification invariants: render rejection, idempotency, preferences, retry, restart durability, and token separation verified"
