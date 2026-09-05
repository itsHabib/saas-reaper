<!-- kickoff:distill -->
# Kickoff — ingress tunnel, the reaped capability that axes ngrok

**Audience:** the implementing agent and the reviewer. This records the decisions the specimen
under `specimens/ingress-tunnel/` was built against; the module's own README is the
behavioral reference.

**Grounding:** distilled from the 2026-09-03 SaaS Reaper review session where the operator asked
for "something to axe ngrok" and the shape was settled in two exchanges, plus the repo law in
`CLAUDE.md` and the webhook and notification specimens' layering.

## Why this capability (the selection principle)

Hosted tunnels are the purest case of the Reaper's filter: priced like infrastructure, built
like a weekend project. A persistent outbound connection, a multiplexer, and a host-based
router are a few hundred lines. The rent is per seat.

The honest complication, recorded so nobody relitigates it: strong free alternatives exist
(frp, bore, rathole, cloudflared), and the prerequisite ngrok actually sells is a public
address, wildcard DNS, and a certificate, which none of the other reaped capabilities needed.
So the tunnel itself is not the differentiator. The two things this repository does that none
of those projects do are the generated appliance and the proof harness. The Terraform pack
collapses the prerequisite into one apply; the harness states invariants and holds the code to
them on every change.

## The one design decision (settled before code)

This is the first specimen with a hard cloud-account dependency in its natural demo path. The
answer: the proof is loopback-only and runs in CI; the AWS pack is the deployment artifact,
validated with Terraform's formatter and validator plus the cross-compile the apply performs,
and never applied by any proof. Nothing in `make tunnel-proof` needs an account.

## Shape decisions

- **Control link is a WebSocket carrying yamux**, not raw TCP. It lets one TLS front (Caddy)
  serve control and edge on 443 with an ordinary wildcard certificate, and it survives
  corporate proxies and load balancers that only pass HTTP.
- **The token is the claim.** An operator claims a subdomain and gets one credential; the agent
  never chooses a name. Claiming a subdomain therefore requires a token by construction.
- **Supersede, then tell the loser.** A second agent with the same credential takes over at
  once. The first is closed with WebSocket status `4001` and exits; without that signal two
  agents sharing a token would fight forever on the reconnect schedule. Revocation closes with
  `4003`. Both are permanent to the agent; every other loss is retried on the public schedule.
- **One fresh stream per request, keep-alives off.** A pooled stream could outlive the link
  that owned it and deliver a request meant for a successor to a superseded agent.
- **Unclaimed and offline look the same** at the edge (502) so the public surface never
  reveals which names exist. Non-tunnel hosts are 404.
- **Forwarded headers are stamped from what the edge saw**, never copied from the visitor.
  Behind Caddy the edge's own client address is loopback; Caddy's forwarded-for is preserved.
- **Presence is volatile by design.** Live links are an in-memory table; a restart empties it
  and agents reconnect. Claims and audit are the only durable state.
- **Lifecycle is one table.** `tunnel.Transition` is total over status and event; the
  exhaustive walk pins 3 statuses and 9 edges. Small on purpose, and honest about it.
- **One lock, audit first.** The first review found that an unserialized attach and revoke
  could leave a revoked claim live, and that supersession closed the incumbent before its
  audit committed. The service now sequences every status change under one mutex and commits
  the audit before the routing table moves; a failed write evicts nobody and records nothing.
- **Links are owned, not borrowed.** An upgraded connection is hijacked from net/http, so the
  request context never ends and `Server.Shutdown` never waits. The accept handler tracks
  every link and ends them all, with status `1001`, before the store closes.
- **Reserved labels are a claim-time rule only.** The edge applies grammar and the routing
  table, so a reservation added after a claim cannot silently shadow a live tunnel.

## The deployment pack

`deploy/aws/` builds the server and a pinned Caddy (with the route53 module, via xcaddy) on the
operator's machine, ships both through a private bucket, mints both API tokens, and boots one
small instance (t4g.nano by default) behind an Elastic IP with the claims database on its own
encrypted volume. Route 53 gets apex and wildcard A records. Caddy obtains a wildcard
certificate through DNS-01 using the instance role and renews it. The control plane is on
8443 and the edge on 443 so `control_cidrs` (your machines) and `edge_cidrs` (visitors) can be
gated separately by the security group; nothing listens on 80. A code change re-ships a
binary that a five-minute timer installs; it never replaces the instance, and a newer AMI
never replaces it on its own.

## Identity ladder

1. Perimeter: `control_cidrs` is "register your IP" for a team. Shipped.
2. Per-claim source addresses in the server, refused at connect time. Next, small.
3. OIDC (Okta or any provider) in front of visitor traffic and the management API, around
   `edge.Proxy`. Later, and the one thing a hosted tunnel sells that the fork adds.

## Cross-specimen proof (deferred)

The best proof in this space is the webhook-delivery specimen delivering real signed webhooks
through a tunnel to the Standard Webhooks receivers over a real certificate. It needs the
applied pack, so it is a documented manual procedure once the webhook specimen lands, not a CI
step.

## Definition of done

- Root `make check` green including the nested module; `make tunnel-demo`,
  `make tunnel-invariants`, and `make tunnel-deploy-check` green.
- `WORK.md` contract valid; README, `REAPER.yaml`, `AGENTS.md`, and `CLAUDE.md` consistent.
- Reviewed PR at a green head; gate and merge are the operator's session.
