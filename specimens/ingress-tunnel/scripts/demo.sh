#!/usr/bin/env bash
set -euo pipefail

proof_label='tunnel demo'
harness_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/harness.sh
source "$harness_dir/harness.sh"

trap finish EXIT
trap 'exit 130' INT TERM

require_free_ports "$control_port" "$edge_port" "$acme_target_port" "$umbrella_target_port" "$diag_port"
build_binaries

boot_server
start_target acme "$acme_target_port"
start_target umbrella "$umbrella_target_port"

acme_token=$(claim_tunnel acme)
umbrella_token=$(claim_tunnel umbrella)
start_agent acme "$acme_token" "$acme_target_port"
start_agent umbrella "$umbrella_token" "$umbrella_target_port"
wait_presence acme live
wait_presence umbrella live

echo "routing: each host reaches only its own local origin"
for name in acme umbrella; do
  host="$name.$domain"
  edge_get "$host" "/whoami?probe=1" > "$work_dir/whoami-$name.json"
  jq -e --arg name "$name" --arg host "$host" '
    .name == $name
      and .path == "/whoami" and .query == "probe=1"
      and .forwardedHost == $host
      and .forwardedProto == "http"
      and (.forwardedFor | startswith("127.0.0.1"))
  ' "$work_dir/whoami-$name.json" > /dev/null || fail "$host did not reach the $name origin with the forwarded triple"
done

echo "bodies: one mebibyte of random bytes crosses the tunnel unchanged"
head -c 1048576 /dev/urandom > "$work_dir/blob"
echo_status=$(curl --silent --header "Host: acme.$domain" --data-binary @"$work_dir/blob" \
  --output "$work_dir/blob.echoed" --write-out '%{http_code}' "$edge_url/echo")
[[ "$echo_status" == 200 ]] || fail "echo returned $echo_status"
cmp --silent "$work_dir/blob" "$work_dir/blob.echoed" || fail "echoed body differs from the sent bytes"

echo "streaming: the first chunk arrives long before the last one is written"
timing=$(curl --silent --no-buffer --header "Host: acme.$domain" --output "$work_dir/stream.txt" \
  --write-out '%{time_starttransfer} %{time_total}' "$edge_url/stream?chunks=5&gap=200ms")
[[ "$(wc -l < "$work_dir/stream.txt" | tr -d ' ')" == 5 ]] || fail "stream delivered $(cat "$work_dir/stream.txt")"
awk -v timing="$timing" 'BEGIN {
  split(timing, t, " ")
  if (t[1] + 0 >= 0.5 || t[2] + 0 < 0.7) {
    print "stream timing " timing " shows buffering" > "/dev/stderr"
    exit 1
  }
}' || fail "streaming was buffered: start-transfer versus total was $timing"

echo "websocket: an upgrade survives edge, link, agent, and origin"
reply=$(PROBE_URL="ws://127.0.0.1:$edge_port/ws" PROBE_HOST="acme.$domain" PROBE_MESSAGE='hello through the tunnel' \
  "$work_dir/proof-websocket")
[[ "$reply" == 'acme:hello through the tunnel' ]] || fail "websocket reply was $reply"

echo "observability: every request is a log line and a series on the loopback diagnostics port"
grep -q 'edge request.*subdomain=acme.*path=/whoami.*status=200' "$work_dir/server.log" ||
  fail "the edge did not log the proxied request"
grep -q 'edge request.*subdomain=acme.*upgraded=true' "$work_dir/server.log" ||
  fail "the edge did not log the websocket upgrade"
metrics > "$work_dir/metrics.txt"
grep -q 'reaper_tunnel_requests_total{status="2xx",subdomain="acme"} [1-9]' "$work_dir/metrics.txt" ||
  fail "the acme requests were not counted"
grep -q 'reaper_tunnel_upgrades_total{subdomain="acme"} 1' "$work_dir/metrics.txt" ||
  fail "the websocket upgrade was not counted"
grep -q 'reaper_tunnel_links_live 2' "$work_dir/metrics.txt" ||
  fail "the live link gauge does not show both agents"
[[ "$(diag_status /debug/pprof/)" == 404 ]] || fail "pprof was reachable without its gate"

echo "audit: the read plane shows presence and lifecycle without any credential"
tunnels > "$work_dir/tunnels.json"
jq -e '
  (.tunnels | length == 2)
    and all(.tunnels[]; .state == "active" and .presence == "live" and .revision == 1)
' "$work_dir/tunnels.json" > /dev/null || fail "tunnel list did not show two live active claims"
audit > "$work_dir/audit.json"
jq -e '
  ([.audit[].kind] | sort == ["claimed", "claimed", "connected", "connected"])
    and all(.audit[] | select(.kind == "claimed"); .actor == "proof")
    and all(.audit[] | select(.kind == "connected"); .actor | startswith("agent:"))
' "$work_dir/audit.json" > /dev/null || fail "audit did not record two claims and two connections"
if grep -q "$acme_token" "$work_dir/tunnels.json" "$work_dir/audit.json"; then
  fail "the read plane leaked an agent credential"
fi
jq -c '.audit | map({sequence, subdomain, kind, actor})' "$work_dir/audit.json"

echo "tunnel demo: two subdomains, two agents, one server; bodies, streams, and websockets pass end to end"
