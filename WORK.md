<!-- reaper-work:v1 -->
# Work: GCP ingress tunnel and Slack pilot proof

Work-ID: ingress-tunnel-gcp
Status: active
Subject: git:1a52161a40c1f143fc5bdaa660d0c63e0b3bef66
Stop-at: reviewed-change

## Outcome

Provide a minimal GCP deployment pack for the existing tunnel and a local Slack-signature
transport proof, preparing a concrete live pilot to replace the operator's hosted tunnel.

## Preserve

- Keep the module independent and preserve the tunnel lifecycle, separate credentials, and loopback diagnostics.
- Keep PR #9 and #10 review fixes separate from this follow-on package.
- Keep automated proofs on loopback 1950x. Operator authorized a separate live GCP validation in the operator-selected project on 2026-09-08; no real Slack changes yet.
- Keep claims and certificate state across VM replacement; never silently delete the state disk.

## Change

- `specimens/ingress-tunnel/deploy/gcp/`: single VM, private artifacts, managed secrets, wildcard TLS/DNS, retained disk, plan tests, cost estimate, live pilot checklist and LIVE-VALIDATION.md evidence.
- `specimens/ingress-tunnel/scripts/`: include GCP validation and a synthetic Slack callback in the demo.
- `specimens/ingress-tunnel/cmd/proof-target/`: fixture-only Slack signature verification.
- `specimens/ingress-tunnel/README.md`: document GCP and the honest local versus live proof boundary.
- `.github/workflows/ci.yml`: name both deployment packs in the existing proof job.
- `AGENTS.md`: include the GCP deployment guide and proof obligations.
- `CLAUDE.md`: preserve byte-identical agent entrypoints.
- `WORK.md`: record this follow-on scope and evidence.

## Prove

- Green: make check and make -C specimens/ingress-tunnel demo invariants deploy-check.
- Green: mocked GCP defaults and retained disk state plan; actual pinned Caddy adapter accepts rendered config.
- Green: valid signed Slack form body reaches the origin and is acknowledged within three seconds.
- Red: tampered/stale signed payloads fail, unrelated domain and cross-zone state move fail, empty control allowlist fails.

## Stop

- Operator now requests landing #9 then #10 then #11 through Gate. Grant minting, judge and resolve remain operator-only; no merge without the granted, pinned Gate command.
- Live GCP deployment is authorized for the project/domain recorded in ignored local deployment variables. Real Slack cutover still needs receiver/test-app inputs; AWS validation still needs account access.
- No further fix rounds on #9 or #10 unless genuinely new reviewer findings warrant them.

## Evidence

- Verified: make check and all three tunnel proofs pass. Eight mocked GCP tests, Terraform-rendered startup scripts, the actual pinned Caddy adapter, and amd64 server build pass.
- Verified: #9 pushed at 17b0cc1 and #10 at 84eb1cd; all seven Codex findings verified and folded, local validation passed on each.
- Verified: GCP Terraform 1.15.8 validates and eight mocked tests pass. CI 1.8.5 failed mocked teardown on prevent_destroy; the tunnel job now pins Terraform 1.15.8 and Go 1.26.5, matching the tested builder. No real infrastructure was applied.

## Handoff

- Last: GCP live deployment passed TLS, signed fixture callbacks, 1 MiB body, WSS, streaming, telemetry, and VM replacement with retained claims and identical certificates. Fixed missing Linux service accounts found by live boot and excluded operator Terraform files from local proof copies. make check and all three proofs pass.
- Next: review the live-bootstrap fixes on #11. Reboot and no-drift plan passed; temporary claim revoked and fixtures stopped. Cloud host remains running. Actual Slack app, AWS deployment, measured bill and full teardown remain unverified. Preserve ignored Terraform state in this worktree; it owns live resources.
