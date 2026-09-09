<!-- reaper-work:v1 -->
# Work: IPv6 ingress feasibility experiment

Work-ID: ingress-tunnel-ipv6-experiment
Status: active
Subject: git:2371c58101c524f02d5f67e504a8438606a50988
Stop-at: operator-decision

## Outcome

Determine whether the GCP tunnel can omit public IPv4 without paid NAT while
preserving IPv4-client access, transport behavior, and authority boundaries.
Package an opt-in exact-host IPv6 deployment and its proofs in a draft PR; the
coordinating agent owns live validation and any approved migration.

## Preserve

- Existing deployment defaults, runtime, DNS, IPv4 address, and private Terraform state.
- Independent tunnel module, loopback-only proofs, separate credentials and scoped DNS authority.
- Public artifacts use generic examples and contain no operator identifiers or secrets.

## Change

- `specimens/ingress-tunnel/experiments/ipv6/`: sourced feasibility result and opt-in public egress probe.
- `specimens/ingress-tunnel/deploy/gcp/`: opt-in IPv6 mode, static address, retained migration reservation, origin secret, Caddy policy, mock tests and setup guide.
- `specimens/ingress-tunnel/scripts/`: render and exercise both deployment modes on loopback.
- `WORK.md`: current bounded outcome and evidence.

## Prove

- Green: shell syntax/lint and controlled transport-response probe checks.
- Red: missing explicit probe flag and failed transport return nonzero.
- Green: make check and all three tunnel proofs preserve current behavior.

## Stop

- No infrastructure apply, DNS/firewall changes, IPv4 release, Slack cutover, or merge.
- No paid NAT or expanded host DNS authority to force feasibility.
- The delegated deep-hostname configuration fails free Universal SSL coverage; do not claim it works.

## Evidence

- Verified: official DNS delegation and Universal SSL documentation falsify the unchanged-domain candidate.
- Verified: make check and all three tunnel proofs pass after packaging; 16 GCP mocks and actual Caddy origin-auth/visitor/forwarding/log/no-store tests pass.
- Verified: Bash syntax, ShellCheck, and controlled curl responses cover reachable HTTP rejection, network failure, empty HTTP response, and missing opt-in.
- Verified: coordinating agent reports isolated no-public-IPv4 E2 boot, first-level TLS, IPv4-forced agent/WSS, exact body, signed fixture callbacks, streaming, access rejection and telemetry. See sanitized LIVE-VALIDATION.md.
- Verified: post-reboot transport/security suite passed; certificates retained, services active, claim revoked and local fixtures stopped.
- Verified: coordinating agent removed experimental cloud/DNS resources and OS Login key; original delegation remained and original pilot strict-TLS health returned 200.
- Remaining production acceptance and current-pilot IPv4 release are not completed.

## Handoff

- Last: implemented opt-in packaging with origin authentication, visitor CIDRs, finite hostnames and migration reservation retention.
- Next: coordinating agent reviews and validates the exact packaged live plan before any IPv4 release.
