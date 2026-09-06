<!-- reaper-work:v1 -->
# Work: Ingress tunnel observability

Work-ID: ingress-tunnel-observability
Status: active
Subject: git:027674a0a022aafc036ee14fc8cd0557ef30892e
Stop-at: reviewed-change

## Outcome

The ingress-tunnel specimen reports what it does: one structured access line
per edge request including refusals and upgrades, a Prometheus endpoint on a
loopback-only diagnostics listener with live links, per-subdomain requests,
bytes, durations, upgrades, and stream-open latency and failures, a pprof
surface on the same listener that exists only behind an explicit gate, and an
AWS pack that writes both services' logs and Caddy's JSON access logs to files,
ships them to CloudWatch, and scrapes the metrics into a CloudWatch namespace.

## Preserve

- Every behavior the tunnel specimen already proves stays unchanged; the observer is a
  side channel and never decides a request's outcome.
- The diagnostics listener binds loopback only; a routable address is refused at startup.
- Only `internal/link` speaks WebSocket or yamux; `internal/metrics` may import only the
  edge's observer contract.
- Demo and CI traffic stays on loopback ports `1950x`; the AWS pack is validated, never applied.

## Change

- `specimens/ingress-tunnel/internal/edge/observe.go`: the consumer-owned `Observer` and the
  response recorder that captures status, bytes, and upgrades without altering the response.
- `specimens/ingress-tunnel/internal/edge/proxy.go`: observe and log every outcome once;
  time every stream open.
- `specimens/ingress-tunnel/internal/metrics/`: the Prometheus registry behind the observer.
- `specimens/ingress-tunnel/cmd/reaper-tunnel/`: `REAPER_TUNNEL_DIAG_ADDR` (loopback only)
  and `REAPER_TUNNEL_PPROF`; a third listener serving metrics and gated pprof.
- `specimens/ingress-tunnel/scripts/`: the demo asserts the access lines, the series, and the
  closed pprof gate; the invariants open the gate for the post-restart boot and check it.
- `specimens/ingress-tunnel/scripts/check-boundaries.sh`: the metrics rule.
- `specimens/ingress-tunnel/deploy/aws/`: Caddy access logs, service logs to files, the
  CloudWatch agent with log shipping and a Prometheus scrape, log groups with retention, the
  managed agent policy, and a `pprof` variable that replaces the host deliberately.
- `specimens/ingress-tunnel/README.md`: the three observability surfaces.
- `specimens/ingress-tunnel/deploy/aws/README.md`: what the log groups and namespace carry.
- `AGENTS.md`: the metrics layering rule and the loopback-only diagnostics listener.
- `CLAUDE.md`: keep the paired agent guide byte-identical.
- `README.md`: one paragraph on the observability surfaces.
- `WORK.md`: this contract.

## Prove

- Green: `make check` passes with the new packages under race, strict lint, shfmt, shellcheck,
  and the extended boundary script.
- Green: `make tunnel-demo` finds the access lines for a proxied request and an upgrade, the
  request and upgrade counters, the live-link gauge at two, and no pprof.
- Green: `make tunnel-invariants` finds pprof absent before the restart and present behind the
  gate after it, and metrics served on the diagnostics port.
- Green: `make tunnel-deploy-check` validates the pack with the agent, log groups, and policy.
- Red: a routable diagnostics address is refused at startup; pprof without the gate is 404.

## Stop

- Stop before any non-loopback proof dependency, cloud account, applied infrastructure, Gate
  invocation, or merge.
- Stop after two review-fix rounds even if a broader finding remains.

## Evidence

- Verified: nested `make check`, `make demo`, `make invariants`, and `make deploy-check` pass
  locally on this branch.

## Handoff

- Last: observability implemented and proven on loopback; pack validated.
- Next: open the pull request against `ingress-tunnel-specimen`, gather review, fold verified
  findings within two rounds, and stop at a reviewed green head.
