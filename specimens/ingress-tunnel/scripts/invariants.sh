#!/usr/bin/env bash
set -euo pipefail

proof_label='tunnel invariants'
harness_dir=$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)
# shellcheck source=scripts/harness.sh
source "$harness_dir/harness.sh"

trap finish EXIT
trap 'exit 130' INT TERM

require_free_ports "$control_port" "$edge_port" "$acme_target_port" "$umbrella_target_port"
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

# Isolation: a host reaches exactly the origin its subdomain names, and every other host shape
# is refused without touching any agent.
[[ "$(whoami_field "acme.$domain" name)" == acme ]] || fail "acme host reached another origin"
[[ "$(whoami_field "umbrella.$domain" name)" == umbrella ]] || fail "umbrella host reached another origin"
[[ "$(edge_status "ghost.$domain" /whoami)" == 502 ]] || fail "an unclaimed subdomain was not refused as offline"
[[ "$(edge_status "$domain" /whoami)" == 404 ]] || fail "the apex was served"
[[ "$(edge_status "deep.acme.$domain" /whoami)" == 404 ]] || fail "a nested label was served"
[[ "$(edge_status "acme.$domain.evil.test" /whoami)" == 404 ]] || fail "a suffix-spoofed host was served"
[[ "$(edge_status "www.$domain" /whoami)" == 404 ]] || fail "a reserved label was served"

# Forwarded headers come from what the edge observed, not from what the visitor sent.
spoofed=$(curl --fail --silent --header "Host: acme.$domain" --header 'X-Forwarded-Host: victim.example' \
  --header 'X-Forwarded-Proto: gopher' "$edge_url/whoami")
jq -e --arg host "acme.$domain" '.forwardedHost == $host and .forwardedProto == "http"' <<< "$spoofed" > /dev/null ||
  fail "spoofed forwarded headers reached the origin: $spoofed"

# Claiming requires a credential the server issued; a malformed or unknown token never attaches
# and the agent stops rather than retrying forever.
start_agent malformed not-a-token "$acme_target_port"
[[ "$(wait_exit "$(agent_pid malformed)" 'malformed-token agent')" != 0 ]] || fail "a malformed token agent exited zero"
grep -q 'status 401' "$work_dir/agent-malformed.log" || fail "the malformed token was not refused with 401"
unknown_token="rtk_$(head -c 32 /dev/urandom | base64 | tr '+/' '-_' | tr -d '=\n')"
start_agent unknown "$unknown_token" "$acme_target_port"
[[ "$(wait_exit "$(agent_pid unknown)" 'unknown-token agent')" != 0 ]] || fail "an unknown token agent exited zero"
grep -q 'status 401' "$work_dir/agent-unknown.log" || fail "the unknown token was not refused with 401"

# Management and read authorities are separate in both directions.
[[ "$(management_status /v1/tunnels '{"subdomain":"extra"}' "$read_token")" == 401 ]] || fail "the read token could claim"
[[ "$(management_status /v1/tunnels '{"subdomain":"extra"}' '')" == 401 ]] || fail "no token could claim"
[[ "$(management_status /v1/tunnels/acme/revoke '{"expectedRevision":1}' "$read_token")" == 401 ]] || fail "the read token could revoke"
[[ "$(read_status /v1/tunnels "$admin_token")" == 401 ]] || fail "the management token could list tunnels"
[[ "$(read_status /v1/audit "$admin_token")" == 401 ]] || fail "the management token could read the audit"
[[ "$(read_status /v1/audit '')" == 401 ]] || fail "no token could read the audit"

# Claims are unique and shaped; refusals never mint a credential.
[[ "$(management_status /v1/tunnels '{"subdomain":"acme"}')" == 409 ]] || fail "a taken subdomain was claimed twice"
[[ "$(management_status /v1/tunnels '{"subdomain":"Bad.Name"}')" == 400 ]] || fail "an invalid subdomain was claimed"
[[ "$(management_status /v1/tunnels '{"subdomain":"www"}')" == 400 ]] || fail "a reserved subdomain was claimed"
[[ "$(tunnels | jq '.tunnels | length')" == 2 ]] || fail "a refused claim left a tunnel behind"

# A second agent with the same credential supersedes the first: traffic moves at once and the
# superseded agent stops instead of fighting back.
start_agent acme-second "$acme_token" "$umbrella_target_port"
moved=false
for _ in {1..200}; do
  if [[ "$(whoami_field "acme.$domain" name 2> /dev/null || true)" == umbrella ]]; then
    moved=true
    break
  fi
  sleep 0.05
done
[[ "$moved" == true ]] || fail "traffic did not move to the superseding agent"
[[ "$(wait_exit "$(agent_pid acme)" 'superseded agent')" != 0 ]] || fail "the superseded agent exited zero"
grep -q 'superseded' "$work_dir/agent-acme.log" || fail "the superseded agent was not told why it stopped"
sleep 0.5
[[ "$(whoami_field "acme.$domain" name)" == umbrella ]] || fail "the superseded agent took the tunnel back"
audit | jq -e '[.audit[] | select(.subdomain == "acme") | .kind] | index("superseded") != null' > /dev/null ||
  fail "supersession was not audited"

# Claims and audit survive a server restart, and agents reconnect on their own schedule.
audit_before=$(audit | jq -cS '.audit')
stop_server
[[ "$(edge_status "acme.$domain" /health)" == 000 ]] || fail "the edge kept answering after the server stopped"
boot_server
wait_presence acme live
wait_presence umbrella live
[[ "$(whoami_field "acme.$domain" name)" == umbrella ]] || fail "acme did not come back through the surviving agent"
[[ "$(whoami_field "umbrella.$domain" name)" == umbrella ]] || fail "umbrella did not come back after the restart"
tunnels | jq -e '(.tunnels | length == 2) and all(.tunnels[]; .revision == 1 and .state == "active")' > /dev/null ||
  fail "claims changed across the restart"
before_count=$(jq 'length' <<< "$audit_before")
audit | jq -cS --argjson count "$before_count" '[.audit[] | select(.sequence <= $count)]' > "$work_dir/audit-kept.json"
[[ "$(cat "$work_dir/audit-kept.json")" == "$audit_before" ]] || fail "audit rows changed across the restart"
audit | jq -e --argjson count "$before_count" '[.audit[] | select(.sequence > $count) | .kind] | sort == ["connected", "connected"]' > /dev/null ||
  fail "the restart did not audit exactly the two reconnections"

# Revocation closes the live link, refuses the credential from then on, and is revision-pinned.
[[ "$(management_status /v1/tunnels/umbrella/revoke '{"expectedRevision":7}')" == 409 ]] || fail "a stale revision revoked"
management_post /v1/tunnels/umbrella/revoke '{"expectedRevision":1}' "$work_dir/revoke.json"
jq -e '.state == "revoked" and .revision == 2 and .presence == "absent" and (.revokedAt | length > 0)' \
  "$work_dir/revoke.json" > /dev/null || fail "revoke response was incomplete"
wait_edge "umbrella.$domain" 502
[[ "$(wait_exit "$(agent_pid umbrella)" 'revoked agent')" != 0 ]] || fail "the revoked agent exited zero"
grep -q 'revoked\|status 401' "$work_dir/agent-umbrella.log" || fail "the revoked agent was not told why it stopped"
[[ "$(management_status /v1/tunnels/umbrella/revoke '{"expectedRevision":2}')" == 409 ]] || fail "a revoked claim revoked twice"
start_agent umbrella-again "$umbrella_token" "$umbrella_target_port"
[[ "$(wait_exit "$(agent_pid umbrella-again)" 'revoked-token agent')" != 0 ]] || fail "a revoked token reconnected"
grep -q 'status 401' "$work_dir/agent-umbrella-again.log" || fail "the revoked token was not refused with 401"
[[ "$(whoami_field "acme.$domain" name)" == umbrella ]] || fail "revoking umbrella disturbed acme"

# The audit is append-only, newest first, attributed to whoever caused each row, and free of
# credential material. The disconnect a revoke forces is the operator's act, so it carries the
# management principal; link events the agent caused carry the agent identity.
audit > "$work_dir/audit-final.json"
jq -e '
  ([.audit[].sequence] | . == (sort | reverse) and length == (unique | length))
    and ([.audit[] | select(.subdomain == "umbrella") | {kind, actor}][0:2]
      == [{kind: "revoked", actor: "proof"}, {kind: "disconnected", actor: "proof"}])
    and all(.audit[] | select(.kind == "claimed" or .kind == "revoked"); .actor == "proof")
    and all(.audit[] | select(.kind == "connected" or .kind == "superseded"); .actor | startswith("agent:"))
' "$work_dir/audit-final.json" > /dev/null || fail "audit ordering or attribution is wrong"
tunnels > "$work_dir/tunnels-final.json"
if grep -q "$acme_token\|$umbrella_token" "$work_dir/audit-final.json" "$work_dir/tunnels-final.json"; then
  fail "the read plane leaked an agent credential"
fi

echo "tunnel invariants: isolation, credential gating, authority separation, supersession, restart durability, revocation, and audit integrity held"
