# Ingress tunnel golden specimen

This module is a SaaS Reaper customer-owned specimen: one Go server, one SQLite file, one
static binary per developer, and no per-seat rent for exposing a local port to the internet.
An operator claims a subdomain and receives a credential. A developer runs the agent with that
credential and a local target. Visitors reach the target at `https://<subdomain>.<domain>`.

The selection rule is the same one that chose feature flags and webhook delivery. Hosted tunnel
vendors price a persistent connection, a multiplexer, and a host-based router as if it were
infrastructure. The mechanism is a few hundred lines. What a vendor actually sells is the
prerequisite: a public address, wildcard DNS, and a certificate. This specimen ships that
prerequisite as a [Terraform pack](deploy/aws/README.md) so the objection collapses into one
`terraform apply`.

The free alternatives are real and this specimen does not pretend otherwise. frp, bore,
rathole, and cloudflared all move bytes. What none of them ship is the pair this repository
insists on: a customer-owned appliance whose deployment is one command, and a runnable proof
harness that states its invariants and holds the code to them on every change.

Factory templates for the tunnel are not part of this specimen; the root factory still
accepts only the `feature-flags` capability.

## Run the proof

From the repository root:

```sh
make tunnel-demo
make tunnel-invariants
# static checks plus both runnable proofs
make tunnel-proof
```

The demo boots the server and two local origins, claims two subdomains, connects one agent
each, and proves from the outside that each host reaches only its own origin with the
forwarded triple intact, that a mebibyte of random bytes crosses unchanged, that a chunked
response streams rather than buffers, that a WebSocket upgrade survives every hop, and that the
read plane shows presence and lifecycle without ever showing a credential.

The invariant harness independently proves:

- a host resolves to exactly one well-formed label beneath the domain; the apex, nested
  labels, and foreign suffixes are refused before any agent is consulted;
- an unclaimed, reserved, or offline subdomain answers the same way, so the edge does not
  reveal which names exist or are claimable;
- forwarded headers are stamped from what the edge observed and cannot be spoofed by a
  visitor; the client chain is kept only when the immediate peer is the loopback TLS front, so
  the origin sees the real visitor address behind Caddy and never a self-declared one;
- a malformed or unknown credential is refused with 401 and the agent stops rather than retry;
- the management token cannot read and the read token cannot write, in both directions;
- a taken, malformed, or reserved subdomain is refused and refusals mint nothing;
- a second agent with the same credential supersedes the first at once, and the superseded
  agent is told why and stops instead of fighting back;
- claims and audit rows survive a server restart on the same file, and agents reconnect on
  their own schedule with exactly the reconnections audited;
- revocation is revision-pinned, closes the live link, refuses the credential from then on,
  and leaves other tunnels untouched;
- the audit is append-only, newest first, attributed to the configured principal or the agent
  identity, and free of credential material.

Dependency installation happens only in `setup`. Demo and invariant execution make no registry
calls and send traffic only to `127.0.0.1` on ports `1950x`.

## Shape

```text
visitor ──HTTPS :443───▶ edge (Host → subdomain) ──stream──▶ link ──WebSocket──▶ agent ──HTTP──▶ target
operator ─HTTPS :8443──▶ control: management API, read API, agent connect
```

The control plane has its own port so a perimeter can treat the two audiences differently:
the pack's `control_cidrs` is your machines, `edge_cidrs` is whoever may reach the tunnels.

- `internal/tunnel` owns policy: subdomain grammar and reserved labels, credential issue and
  hashing, the in-memory registry of live links with supersession by generation, the
  lifecycle table, host resolution, and the reconnect schedule. It imports no mechanism. One
  mutex in the service sequences every status change, and the audit row is committed before
  the routing table moves, so a failed write evicts nobody and records nothing.
- `internal/link` is the control connection: one WebSocket per agent, multiplexed with yamux.
  The server opens one stream per request; the agent accepts them. Only this package knows
  either protocol. Links are hijacked from net/http, so the handler owns their lifetime and
  ends every one, with a stated reason, before the process lets the store close.
- `internal/edge` is the public ingress: it resolves the host, looks up the live link, and
  reverse-proxies the request down a fresh stream. Streaming, upgrades, and forwarded headers
  are the standard library's proxy with keep-alives disabled so a pooled stream can never
  outlive the link that owns it.
- `internal/agent` is the customer side: hold one link, serve every stream into the local
  target, reconnect on the policy schedule when the link is lost, and stop when evicted.
- `internal/api` is the management and read transport; `internal/store/sqlite` persists claims
  and audit; `cmd/reaper-tunnel` and `cmd/reaper-tunnel-agent` are the composition roots.

## Observability

Three surfaces, all on the host, none reachable from outside it:

- **Access log.** The edge writes one structured line per request: subdomain, host, method,
  path, status, bytes, duration, whether it was a WebSocket upgrade, and the peer address.
  Refusals are logged the same way, so a scan of nonexistent names is visible.
- **Metrics.** A Prometheus endpoint on the loopback diagnostics listener
  (`REAPER_TUNNEL_DIAG_ADDR`, default `127.0.0.1:8082`): live links, requests and bytes per
  subdomain by status class, request duration, upgrades, stream-open latency, and stream-open
  failures, plus the Go runtime and process collectors. Unresolvable hosts are grouped under
  the `none` subdomain so an enumeration attempt is one series, not thousands.
- **pprof.** Mounted on the same listener only when `REAPER_TUNNEL_PPROF=1`. The server
  refuses a diagnostics address that is not loopback, so neither surface can be exposed by a
  configuration slip.

The AWS pack writes the server log and Caddy's JSON access logs to files, ships them to two
CloudWatch log groups, and scrapes the metrics endpoint into a CloudWatch namespace, so an
instance replacement loses no history.

## The lifecycle table

One table in `tunnel.Transition` decides every status change and the audit rows that record
it. Policy calls it on connect, link loss, and revoke; the exhaustive walk in
`state_walk_test.go` pins three reachable statuses, nine edges, the audit vocabulary each edge
emits, and the fact that a revoked claim never carries a live link.

```text
status            connect                        link-lost         revoke
active/absent  →  active/live  [connected]        (no change)       revoked/absent [revoked]
active/live    →  active/live  [superseded,       active/absent     revoked/absent [disconnected,
                                connected]        [disconnected]                    revoked]
revoked/absent →  refused (revoked)               (no change)       refused (conflict)
```

Supersession is deliberate: a second agent with the same credential takes over, and the
first is closed with WebSocket status `4001`. A revoked agent is closed with `4003`. A server
going away closes with `1001`. The agent treats the first two as permanent and exits; every
other loss is retried on the public schedule `1s, 2s, 5s, 10s, 30s` with the last delay
repeating forever. An eviction waits two seconds for the agent to acknowledge the close frame
and then cuts the socket, so a wedged agent cannot keep its streams alive. The close frame is
best effort, so the server also enforces the outcome: for one minute after a takeover, a
connect against the live link is refused with `409`, which the agent treats as final. A
loser that missed its `4001` therefore stops on its next dial instead of taking the claim
back, and two agents sharing a credential can never flap.

## Authority

Three credentials, never interchangeable:

- the management token claims and revokes; it cannot list tunnels or read the audit;
- the read token lists tunnels with their presence and reads the audit; it cannot write;
- an agent token proves one claim; it is shown once at claim time and only its hash is stored.

The audit actor is whoever caused the row: the configured management principal for claims,
revocations, and the disconnect a revoke forces; the agent identity `agent:<subdomain>` for
connections, supersessions, and losses the agent caused. Request bodies never choose an actor.

## Own it

Fork this module and the pack is the whole rollout: apply it in your account, hand each
developer one claim, and revoke claims as people leave. Identity grows in three rungs, and the
specimen stands on the first:

1. **The perimeter.** `control_cidrs` in the pack is "register your IP" for a team: only those
   addresses can attach an agent or call the management API.
2. **Per-claim source addresses.** A claim carrying its own allowed CIDRs, refused at connect
   time by the control plane. It is "register your IP" per person, revocable per claim, and a
   contained change to the claim policy and the accept handler.
3. **A company identity provider.** OIDC in front of visitor traffic and the management API.
   The seam is `edge.Proxy`: an authenticating handler wrapped around it sees the resolved host
   and the visitor before any stream is opened.

The last two are the things a hosted tunnel sells that this specimen leaves for the fork, and
they are documented as such rather than shipped half-built.
