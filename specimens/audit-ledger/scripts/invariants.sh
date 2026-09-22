#!/usr/bin/env bash
set -euo pipefail

service_port=19602
write_token=invariants-write-token
write_principal=invariants-ingest
read_token=invariants-read-token
read_tenants=acme,globex

# shellcheck source=scripts/harness.sh
source "$(dirname "${BASH_SOURCE[0]}")/harness.sh"

fail() {
  echo "audit invariant failed: $1" >&2
  exit 1
}

event() {
  local tenant=$1
  local id=$2
  local actor=${3:-user:ada}
  jq -cn --arg tenant "$tenant" --arg id "$id" --arg actor "$actor" '{
    tenant: $tenant,
    id: $id,
    actor: $actor,
    action: "document.viewed",
    target: ("document:" + $id),
    occurredAt: "2026-08-30T09:00:00Z",
    metadata: {index: ($id | ltrimstr("evt-") | tonumber)}
  }'
}

head_of() {
  read_head "$1" | jq -c '{sequence, hash}'
}

build_service
boot_service

batch=$(jq -cs '.' <(event acme evt-1) <(event acme evt-2) <(event acme evt-3) <(event acme evt-4) <(event globex evt-1))

write_with_read=$(response_status \
  --request POST \
  --header "Authorization: Bearer $read_token" \
  --header 'Content-Type: application/json' \
  --data-binary "$batch" \
  "$base_url/v1/events")
[[ "$write_with_read" == 401 ]] || fail "read token appended with status $write_with_read, want 401"
read_with_write=$(response_status \
  --header "Authorization: Bearer $write_token" \
  "$base_url/v1/tenants/acme/head")
[[ "$read_with_write" == 401 ]] || fail "write token read a head with status $read_with_write, want 401"
read_with_none=$(response_status "$base_url/v1/tenants/acme/export")
[[ "$read_with_none" == 401 ]] || fail "missing token exported with status $read_with_none, want 401"
echo "token separation: the write authority cannot read and the read authority cannot append"

status=$(ingest "$batch" "$work_dir/first.json")
[[ "$status" == 201 ]] || fail "first batch returned $status, want 201"
jq -e '[.receipts[] | select(.tenant == "acme") | .sequence] == [1, 2, 3, 4]' "$work_dir/first.json" > /dev/null ||
  fail "first batch did not assign acme sequences 1-4"
head_before=$(head_of acme)
[[ "$(jq -r '.sequence' <<< "$head_before")" == 4 ]] || fail "acme head is not at sequence 4"

status=$(ingest "$batch" "$work_dir/replay.json")
[[ "$status" == 200 ]] || fail "identical replay returned $status, want 200"
jq -e 'all(.receipts[]; .replayed == true)' "$work_dir/replay.json" > /dev/null ||
  fail "replayed receipts were not marked replayed"
first_positions=$(jq -c '[.receipts[] | {tenant, id, sequence, hash}]' "$work_dir/first.json")
replay_positions=$(jq -c '[.receipts[] | {tenant, id, sequence, hash}]' "$work_dir/replay.json")
[[ "$first_positions" == "$replay_positions" ]] || fail "replay returned different positions or hashes"
[[ "$(head_of acme)" == "$head_before" ]] || fail "identical replay moved the acme head"
echo "idempotency: a replayed batch returned the original positions and appended nothing"

conflict=$(event acme evt-2 user:mallory)
status=$(ingest "$conflict" "$work_dir/conflict.json")
[[ "$status" == 409 ]] || fail "replay with different content returned $status, want 409"
[[ "$(head_of acme)" == "$head_before" ]] || fail "conflicting replay moved the acme head"
echo "conflict: a replayed id with different content was refused without appending"

invalid=$(jq -cs '.[1].metadata.ratio = 0.5 | .' <(event acme evt-10) <(event acme evt-11))
status=$(ingest "$invalid" "$work_dir/invalid.json")
[[ "$status" == 400 ]] || fail "batch with a non-integer number returned $status, want 400"
mixed=$(jq -cs '.' <(event acme evt-10) <(event acme evt-2 user:mallory))
status=$(ingest "$mixed" "$work_dir/mixed.json")
[[ "$status" == 409 ]] || fail "batch with a conflicting replay returned $status, want 409"
[[ "$(head_of acme)" == "$head_before" ]] || fail "a rejected batch still appended its valid events"
curl --fail --silent --show-error --header "Authorization: Bearer $read_token" \
  "$base_url/v1/tenants/acme/events?after=0&limit=100" > "$work_dir/after-rejections.json"
jq -e '[.events[].id] == ["evt-1", "evt-2", "evt-3", "evt-4"]' "$work_dir/after-rejections.json" > /dev/null ||
  fail "a rejected batch leaked an event into the chain"
echo "atomic batch: one invalid or conflicting event kept the whole batch out"

status=$(ingest "$(event initech evt-1)" "$work_dir/initech.json")
[[ "$status" == 201 ]] || fail "append to an unscoped tenant returned $status, want 201"
for route in head events export; do
  curl --silent --output "$work_dir/initech-$route.txt" --write-out '%{http_code}' \
    --header "Authorization: Bearer $read_token" \
    "$base_url/v1/tenants/initech/$route" > "$work_dir/initech-$route.status"
  curl --silent --output "$work_dir/nobody-$route.txt" --write-out '%{http_code}' \
    --header "Authorization: Bearer $read_token" \
    "$base_url/v1/tenants/nobody/$route" > "$work_dir/nobody-$route.status"
  [[ "$(cat "$work_dir/initech-$route.status")" == 404 ]] ||
    fail "$route for an unscoped existing tenant returned $(cat "$work_dir/initech-$route.status"), want 404"
  [[ "$(cat "$work_dir/nobody-$route.status")" == 404 ]] ||
    fail "$route for an absent tenant returned $(cat "$work_dir/nobody-$route.status"), want 404"
  cmp -s "$work_dir/initech-$route.txt" "$work_dir/nobody-$route.txt" ||
    fail "$route response bodies differ between an unscoped tenant and an absent tenant"
done
echo "tenant isolation: an out-of-scope tenant is indistinguishable from an absent one"

more=$(jq -cs '.' <(event acme evt-5) <(event acme evt-6) <(event acme evt-7) <(event acme evt-8) \
  <(event acme evt-9) <(event acme evt-10) <(event acme evt-11) <(event acme evt-12) \
  <(event acme evt-13) <(event acme evt-14))
status=$(ingest "$more" "$work_dir/more.json")
[[ "$status" == 201 ]] || fail "second acme batch returned $status, want 201"
after=0
: > "$work_dir/walk.txt"
for _ in {1..20}; do
  curl --fail --silent --show-error --header "Authorization: Bearer $read_token" \
    "$base_url/v1/tenants/acme/events?after=$after&limit=3" > "$work_dir/page.json"
  count=$(jq '.events | length' "$work_dir/page.json")
  jq -r '.events[].sequence' "$work_dir/page.json" >> "$work_dir/walk.txt"
  after=$(jq -r '.next' "$work_dir/page.json")
  if [[ "$count" == 0 ]]; then
    break
  fi
done
[[ "$(paste -sd ' ' "$work_dir/walk.txt")" == "$(seq 1 14 | paste -sd ' ' -)" ]] ||
  fail "pagination walk produced [$(paste -sd ' ' "$work_dir/walk.txt")], want 1..14 exactly once"
[[ "$after" == 14 ]] || fail "final page cursor is $after, want 14"
echo "pagination: every sequence appeared exactly once with no gaps"

head_before=$(head_of acme)
read_export acme "$work_dir/export-before.ndjson"
stop_service

python3 - "$database_path" > "$work_dir/sql-probe.txt" << 'PY'
import sqlite3
import sys

connection = sqlite3.connect(sys.argv[1])
before = connection.execute("SELECT count(*) FROM entries").fetchone()[0]
for statement in (
    "UPDATE entries SET actor = 'mallory' WHERE tenant = 'acme' AND sequence = 1",
    "DELETE FROM entries WHERE tenant = 'acme' AND sequence = 1",
):
    try:
        connection.execute(statement)
        connection.commit()
        print("accepted: " + statement)
        continue
    except sqlite3.IntegrityError as error:
        print("rejected: " + str(error))
after = connection.execute("SELECT count(*) FROM entries").fetchone()[0]
print("rows %d -> %d" % (before, after))
PY
grep -c 'rejected: audit ledger is append-only' "$work_dir/sql-probe.txt" | grep -qx 2 ||
  fail "SQL layer accepted a mutation: $(cat "$work_dir/sql-probe.txt")"
grep -qx 'rows 16 -> 16' "$work_dir/sql-probe.txt" ||
  fail "row count changed under a rejected mutation: $(cat "$work_dir/sql-probe.txt")"
echo "append-only: SQLite triggers refused UPDATE and DELETE on the entries table"

boot_service
[[ "$(head_of acme)" == "$head_before" ]] || fail "restart changed the acme head"
read_export acme "$work_dir/export-after.ndjson"
cmp -s "$work_dir/export-before.ndjson" "$work_dir/export-after.ndjson" ||
  fail "restart changed the acme export"
report=$(verify_export "$work_dir/export-after.ndjson") || fail "verifier rejected the post-restart export: $report"
[[ "$report" == "ok sequence=14 head=$(jq -r '.hash' <<< "$head_before")" ]] ||
  fail "post-restart verifier report disagrees with the pre-restart head: $report"
echo "restart: head, sequences, and export bytes survived the same SQLite authority restart"

echo "audit invariants: token separation, idempotency, atomic batches, tenant isolation, pagination, append-only SQL, and restart durability verified"
