# GCP live validation

Operator-authorized deployment on 2026-09-08 (America/Los_Angeles), from PR #11.
Host shape: `us-central1-a`, `e2-micro`, with a static IPv4 address.
The operator domain delegates a subzone from Cloudflare to a GCP parent zone;
Terraform manages a dedicated tunnel child zone and the wildcard beneath it.
This guide uses generic deployment references. Exact account identifiers, hostnames,
IPs and local artifact paths belong in private operator records.

## Verified live

- Terraform created the real infrastructure. Startup retrieved artifacts and secrets
  through the VM service account; both systemd services became active.
- Public HTTPS validates normally, without certificate bypass. Both control and
  wildcard edge certificates were issued. Public DNS resolves to the static IP.
- A temporary `liveproof` claim routes to a fixture on the operator's Mac at
  `127.0.0.1:19502`. A 1 MiB random body returns byte-for-byte unchanged.
- WSS upgrade and echo work through Caddy, edge, control link, agent, and origin.
- Streaming delivers the first chunk before the remaining four; one measured run
  delivered the first at 1.651 s and completed at 2.842 s with 300 ms chunk gaps.
- A synthetic Slack v0 signed raw form is acknowledged with 200 in 0.324 s;
  altered body and stale timestamp return 401 (0.272 s and 0.279 s). This uses the
  proof receiver and an ephemeral signing secret, not a real Slack app.
- Spoofed forwarded-for/proto values do not reach the origin as trusted values.
- Read credentials cannot create claims; tunnel listing does not reveal the agent token.
- Offline and unclaimed valid tunnel names produce identical 502 responses.
- Prometheus shows live links, requests by status, response bytes, and upgrades.
  Diagnostics binds `127.0.0.1:8082`; pprof returns 404. Service state directories
  are 0750, owned separately by 65532 and 65531; the generated env file is root:root 0600.

- A second deliberate VM replacement changed the instance ID while preserving the public IP.
  The running Mac agent reconnected without a new claim or credential; all traffic
  checks passed again. Both control and wildcard certificate DER SHA-256 fingerprints
  were identical before and after replacement, proving certificate storage was reused.
- Temporary SSH access was restricted to the operator IP for inspection. Its firewall
  rule was deleted; only the intended 443 and restricted 8443 rules remain. The SSH
  public key was uploaded with a 15-minute expiry.

- An explicit VM reset then exercised repeated startup with the same Linux accounts
  and mounted disk. The same agent reconnected and the full traffic probe passed again.
  The durable audit retained exactly one original claim across replacement and reset.
- Final Terraform plan returned exit 0 (no changes). The temporary claim was revoked
  and local fixture/agent stopped; the cloud host remains running for the Slack pilot.

## Defects found by live execution

The first VM failed to start both services with systemd `217/USER`: the pack had
numeric service UIDs but no corresponding Linux accounts. Startup now explicitly
creates and validates the two accounts and refuses identity collisions. The next
fresh VM successfully started both services and served HTTPS.

Running local proof after an apply also exposed that its temporary copy included
operator Terraform files. The harness now excludes state, plans, variables, and
crash logs. Root `make check` and all three tunnel proofs pass after these fixes.

## Remaining limits

- Real Slack callbacks, application idempotency and production cutover are untested.
- AWS is still not live-validated. Its numeric service identities merit the same
  bootstrap check; a working GCP deployment does not validate the AWS image.
- No measured bill, prolonged load test, uptime observation, backup/restore drill,
  or full teardown has been completed. The retained disk deliberately blocks destroy.
- Guest-agent serial output includes denied Cloud Logging writes. Application logs
  remain local in journald as designed; no cloud log export or alerting is claimed.
- Resource-level IAM bindings were inspected and bootstrap exercised. This is not
  an adversarial service-account compromise or negative IAM authorization test.

## Reproduction and custody

Run the README deployment from an authenticated operator host. Keep Terraform state,
plans and credentials private. The standard `demo invariants deploy-check` targets
remain loopback-only and do not deploy anything. For public traffic, build the existing
`cmd/proof-target`, `cmd/proof-websocket`, and `cmd/reaper-tunnel-agent`, create a
separate test claim, and send the same body/signature/stream/WebSocket probes through
its public hostname. Never point this fixture at the production Slack app.

Local raw evidence and the temporary probe script remain in private operator storage.
That directory also contains an ephemeral test credential and must not be uploaded
wholesale. Live Terraform state is in the ignored deployment directory; preserve it
when moving the worktree.
