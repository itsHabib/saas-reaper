<!-- reaper-work:v1 -->
# Work: IPv6 ingress feasibility experiment

Work-ID: ingress-tunnel-ipv6-experiment
Status: active
Subject: git:2371c58101c524f02d5f67e504a8438606a50988
Stop-at: operator-decision

## Outcome

Determine whether the GCP tunnel can omit public IPv4 without paid NAT while
preserving IPv4-client access, transport behavior, and authority boundaries.
Publish an honest experiment and live acceptance plan in a draft PR.

## Preserve

- Existing deployment defaults, runtime, DNS, IPv4 address, and private Terraform state.
- Independent tunnel module, loopback-only proofs, separate credentials and scoped DNS authority.
- Public artifacts use generic examples and contain no operator identifiers or secrets.

## Change

- `specimens/ingress-tunnel/experiments/ipv6/`: sourced feasibility result and opt-in public egress probe.
- `specimens/ingress-tunnel/deploy/gcp/README.md`: link the experiment.
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
- Verified: make check and make -C specimens/ingress-tunnel demo invariants deploy-check pass.
- Verified: Bash syntax, ShellCheck, and controlled curl responses cover reachable HTTP rejection, network failure, empty HTTP response, and missing opt-in.
- Live IPv6 acceptance of a separate first-level-host candidate remains unperformed.

## Handoff

- Last: completed sourced feasibility report, probe, positive/negative checks, and existing tunnel proofs.
- Next: review the draft and choose the isolated live candidate; live experimentation remains separately coordinated.
