#!/usr/bin/env bash
set -euo pipefail

proof_label='notification demo'
harness_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/harness.sh
source "$harness_dir/harness.sh"

trap finish EXIT
trap 'exit 130' INT TERM

slack_port=19402
webhook_url=http://127.0.0.1:$slack_port/services/T0PROOF/B0PROOF/demo

require_free_ports "$service_port" "$smtp_port" "$slack_port"
build_binaries

start_smtp_sink main "$smtp_port" "$work_dir/smtp-receipts.jsonl" 0
start_slack_sink main "$slack_port" "$work_dir/slack-receipts.jsonl" 0
boot_service

register_channel email smtp
register_channel chat slack-webhook

create_template invoice-paid email \
  'Invoice {{invoice.id}} paid' \
  'Hello {{customer.name}},

{{invoice.amount}} {{invoice.currency}} received. Paid: {{invoice.paid}}
'
create_template invoice-paid chat \
  '' \
  '{{customer.name}} paid invoice {{invoice.id}}: {{invoice.amount}} {{invoice.currency}}'

jq -e '
  .variables == ["customer.name", "invoice.amount", "invoice.currency", "invoice.id", "invoice.paid"]
' "$work_dir/template-invoice-paid-email.json" > /dev/null ||
  fail "email template did not record every subject and body variable"

create_recipient cus_acme "$(jq -cn --arg webhook "$webhook_url" '{
  id: "cus_acme",
  channels: [
    {channel: "email", address: "billing@acme.example"},
    {channel: "chat", address: $webhook}
  ]
}')"

send_notification invoice-paid-demo cus_acme "$work_dir/send.json"
notification_id=$(jq -er '.notificationId' "$work_dir/send.json")
jq -e '
  (.notificationId | startswith("ntf_"))
    and .deduplicated == false
    and (.deliveries | length == 2)
    and ([.deliveries[].channel] | sort == ["chat", "email"])
    and ([.deliveries[].id] | unique | length == 2)
' "$work_dir/send.json" > /dev/null || fail "send did not fan out to both allowed channels"

wait_receipts "$work_dir/smtp-receipts.jsonl" 1 "SMTP sink"
wait_receipts "$work_dir/slack-receipts.jsonl" 1 "Slack sink"
wait_attempts "$notification_id" 2 "$work_dir/attempts.json"

expected_subject='Invoice inv_20260901_001 paid'
# printf -v keeps the trailing newline that command substitution would strip.
printf -v expected_body 'Hello Acme & Co <billing>,\n\n4200.50 usd received. Paid: true\n'
expected_text='Acme &amp; Co &lt;billing&gt; paid invoice inv_20260901_001: 4200.50 usd'
email_delivery=$(jq -er '.deliveries[] | select(.channel == "email") | .id' "$work_dir/send.json")
chat_delivery=$(jq -er '.deliveries[] | select(.channel == "chat") | .id' "$work_dir/send.json")

jq -s -e \
  --arg subject "$expected_subject" \
  --arg body "$expected_body" \
  --arg notification "$notification_id" \
  --arg delivery "$email_delivery" '
  length == 1
    and .[0].subject == $subject
    and .[0].body == $body
    and .[0].messageId == ("<" + $delivery + "@reaper.example>")
    and .[0].notificationId == $notification
    and .[0].attempt == "1"
    and .[0].to == ["billing@acme.example"]
    and .[0].from == "notifications@reaper.example"
    and .[0].rejected == false
' "$work_dir/smtp-receipts.jsonl" > /dev/null ||
  fail "the SMTP server did not receive the rendered subject and body"

jq -s -e --arg text "$expected_text" --arg delivery "$chat_delivery" '
  length == 1
    and .[0].valid == true
    and .[0].violation == null
    and .[0].text == $text
    and .[0].deliveryId == $delivery
    and .[0].attempt == "1"
' "$work_dir/slack-receipts.jsonl" > /dev/null ||
  fail "the Slack-compatible receiver did not accept the documented payload shape"

jq -e --arg notification "$notification_id" '
  (.attempts | length == 2)
    and (all(.attempts[];
      .notificationId == $notification
        and .recipientId == "cus_acme"
        and .actor == "proof"
        and .number == 1
        and .outcome == "delivered"
        and .state == "delivered"
        and (has("nextAttemptAt") | not)))
    and ([.attempts[].channelId] | sort == ["chat", "email"])
    and ([.attempts[].sequence] | unique | length == 2)
' "$work_dir/attempts.json" > /dev/null || fail "the durable audit did not record both deliveries exactly once"

rejected_status=$(send_status invoice-paid-missing cus_acme '{"invoice":{"id":"inv_2"}}')
[[ "$rejected_status" == 400 ]] ||
  fail "a send missing a template variable returned $rejected_status, want 400"
grep -q 'is missing from payload' "$work_dir/send-status-body.json" ||
  fail "the rejection did not name the missing template variable"
sleep 0.4
jq -s -e 'length == 1' "$work_dir/smtp-receipts.jsonl" > /dev/null ||
  fail "the rejected send reached the SMTP server"
jq -s -e 'length == 1' "$work_dir/slack-receipts.jsonl" > /dev/null ||
  fail "the rejected send reached the Slack-compatible receiver"

echo "SMTP proof: emersion/go-smtp accepted one message and parsed the rendered subject and body"
echo "Slack proof: the incoming-webhook receiver validated the documented payload shape and text"
echo "notification demo: one send fanned out to two customer-owned transports and audited both"
