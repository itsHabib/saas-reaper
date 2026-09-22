#!/usr/bin/env bash
set -euo pipefail

service_port=19601
write_token=demo-write-token
write_principal=demo-ingest
read_token=demo-read-token
read_tenants=acme,globex

# shellcheck source=scripts/harness.sh
source "$(dirname "${BASH_SOURCE[0]}")/harness.sh"

fail() {
  echo "audit demo failed: $1" >&2
  exit 1
}

build_service
boot_service

status=$(ingest @fixtures/events.json "$work_dir/receipts.json")
[[ "$status" == 201 ]] || fail "fixture ingest returned $status, want 201"
jq -e '
  (.receipts | length == 6)
    and ([.receipts[] | select(.tenant == "acme") | .sequence] == [1, 2, 3, 4, 5])
    and ([.receipts[] | select(.tenant == "globex") | .sequence] == [1])
    and (all(.receipts[]; .replayed == false and (.hash | length == 64)))
' "$work_dir/receipts.json" > /dev/null || fail "receipts did not assign contiguous per-tenant sequences"
echo "ingest: six fixture events chained into two tenant ledgers"

for tenant in acme globex; do
  read_head "$tenant" > "$work_dir/$tenant-head.json"
  service_head=$(jq -er '.hash' "$work_dir/$tenant-head.json")
  service_sequence=$(jq -er '.sequence' "$work_dir/$tenant-head.json")
  read_export "$tenant" "$work_dir/$tenant-export.ndjson"
  line_count=$(wc -l < "$work_dir/$tenant-export.ndjson" | tr -d ' ')
  [[ "$line_count" == "$service_sequence" ]] ||
    fail "$tenant export has $line_count rows, head reports $service_sequence"
  report=$(verify_export "$work_dir/$tenant-export.ndjson") ||
    fail "independent verifier rejected the untouched $tenant export: $report"
  [[ "$report" == "ok sequence=$service_sequence head=$service_head" ]] ||
    fail "$tenant verifier report [$report] disagrees with service head $service_sequence/$service_head"
  echo "agreement: python verifier recomputed $tenant head $service_head from ${service_sequence} rows"
done

tamper_sequence=3
jq -c --argjson at "$tamper_sequence" \
  'if .sequence == $at then .metadata.alpha = "tampered" end' \
  "$work_dir/acme-export.ndjson" > "$work_dir/acme-tampered.ndjson"
if cmp -s "$work_dir/acme-export.ndjson" "$work_dir/acme-tampered.ndjson"; then
  fail "tamper probe did not change the export"
fi
if report=$(verify_export "$work_dir/acme-tampered.ndjson"); then
  fail "verifier accepted a tampered export: $report"
fi
[[ "$report" == "broken sequence=$tamper_sequence reason=hash-mismatch"* ]] ||
  fail "verifier did not locate the tampered row: $report"
echo "tamper probe: one edited metadata value broke the chain exactly at sequence $tamper_sequence"

sed '2d' "$work_dir/acme-export.ndjson" > "$work_dir/acme-gapped.ndjson"
if report=$(verify_export "$work_dir/acme-gapped.ndjson"); then
  fail "verifier accepted an export with a removed row: $report"
fi
[[ "$report" == "broken sequence=3 reason=gap-after-1" ]] ||
  fail "verifier did not locate the removed row: $report"
echo "removal probe: a deleted row surfaced as a gap after sequence 1"

echo "audit demo: independent verifier agrees with the service head and detects tampering"
