# IPv6 cost experiment: feasibility before deployment

Status: **two exact first-level hostnames passed a bounded live IPv6 experiment**.
The unchanged delegated-domain deployment plus free Cloudflare proxy remains
nonviable. This PR contains research, sanitized [live evidence](LIVE-VALIDATION.md),
and an opt-in egress probe. The [GCP IPv6 mode](../../deploy/gcp/IPV6.md) now packages
the exact-host design with origin authentication; direct IPv4 defaults remain unchanged. Source review date: 2026-09-08. Remaining live acceptance below
is not implied by the successful subset.

## Hypothesis and result

Hypothesis: remove the VM's external IPv4, proxy its external IPv6 through
Cloudflare Free, and retain public HTTPS and the agent's control WebSocket from
IPv4-only networks without a paid NAT, load balancer, or certificate service.

The current GCP pack creates a delegated Cloud DNS zone such as
`tunnel.example.com` and serves `slack.tunnel.example.com`. Records in that zone
are resolved by Cloud DNS; putting AAAA there does not place Cloudflare in the
traffic path. Cloudflare's free Universal SSL on a full `example.com` zone covers
`example.com` and `*.example.com`, **not** `slack.tunnel.example.com`.
A wildcard DNS record does not extend certificate coverage.
[Delegation](https://developers.cloudflare.com/dns/manage-dns-records/how-to/subdomains-outside-cloudflare/),
[Universal SSL limitations](https://developers.cloudflare.com/ssl/edge-certificates/universal-ssl/limitations/).

Registering the child as a separate Cloudflare zone is an Enterprise feature;
advanced certificates can cover deeper names but are an additional purchase.
These are not assumed free workarounds. A CNAME alias alone also does not change
the hostname checked by TLS or the tunnel's host router.
[Subdomain setup](https://developers.cloudflare.com/dns/zone-setups/subdomain-setup/),
[advanced certificates](https://developers.cloudflare.com/ssl/edge-certificates/advanced-certificate-manager/).

## Narrower candidate and live result

The live experiment used two explicitly selected first-level names on an existing
registered domain on Cloudflare full setup: `control.example.com:8443` and
`probe.example.com:443`. The server domain remained `example.com`; only those
two proxied AAAA records were needed. Exact per-host HTTP-01 certificates avoided
wildcard issuance and a new domain purchase. See [live evidence](LIVE-VALIDATION.md).
A reusable arbitrary-claim deployment would need wildcard routing/certificates or
an explicitly designed per-claim issuance process. Do not reuse the delegated
child pack unchanged or move an existing domain's authority implicitly.
Cloudflare publishes proxy addresses instead of the origin address; IPv4 clients
must reach Cloudflare, never an origin-only AAAA record.
[Proxy status](https://developers.cloudflare.com/dns/proxy-status/),
[IPv6 compatibility](https://developers.cloudflare.com/network/ipv6-compatibility/).

GCP dual-stack interfaces may retain internal IPv4 while omitting external IPv4.
That is the candidate here, not a requirement to remove every private IPv4 address.
External IPv6 needs a compatible subnet, VM interface, route and network tier;
the isolated E2 micro boot passed, while reusable Terraform planning remains work.
Metadata and Private Google Access may still use internal IPv4 without public NAT.
[Address types](https://docs.cloud.google.com/compute/docs/ip-addresses),
[IPv6 configuration](https://docs.cloud.google.com/compute/docs/ip-addresses/configure-ipv6-address),
[Private Google Access](https://docs.cloud.google.com/vpc/docs/configure-private-google-access).

Cloudflare supports HTTPS on 8443 and WebSockets on all plans. The live IPv4-forced agent
transport passed, but does not prove a corporate firewall permits 8443.
Cloudflare can close idle WebSockets and restart connections during updates; test
heartbeats and agent reconnects. HTTP proxy read/write limits and upload limits
also constrain the public edge. Disable caching for both planes and reject any
interactive challenge on callbacks or agent connections.
[Ports](https://developers.cloudflare.com/fundamentals/reference/network-ports/),
[WebSockets](https://developers.cloudflare.com/network/websockets/),
[connection limits](https://developers.cloudflare.com/fundamentals/reference/connection-limits/).

## Trust and certificates are part of the experiment

Use Full (strict) with an origin certificate matching the original Host/SNI for
each actual control and edge hostname. Origin CA certificates are not a substitute
for the browser-facing edge certificate, and direct rollback clients must trust
the origin certificate. Decide certificate issuance/renewal before boot: the
existing Cloud DNS challenge integration cannot mutate Cloudflare-authoritative
records. A separately delegated ACME challenge zone could preserve narrow DNS
permissions, but requires its own adapter and renewal proof. Do not install a
parent-zone write token on the VM to make the experiment pass.
[Strict TLS requirements](https://developers.cloudflare.com/ssl/origin-configuration/ssl-modes/full-strict/).

The existing VM control allowlist sees Cloudflare's origin-facing addresses, not
the laptop. Preserve its intent with an enforced visitor-IP rule before proxying
and origin ingress restricted to current Cloudflare IPv6 ranges. Prove denied
visitors cannot reach control. Merely allowing Cloudflare makes control reachable
to every visitor Cloudflare forwards. Shared Cloudflare ranges alone also do not
establish identity of this specific zone; assess authenticated origin protection.
Do not trust arbitrary `CF-Connecting-IP` or `X-Forwarded-For`. The experimental
Caddy enforced the visitor allowlist and rewrote the forwarded address; denied-source and spoofing checks passed. A reusable implementation
still needs reviewed trusted-proxy configuration and direct-origin rejection
proofs. The unchanged pack may report the Cloudflare peer instead of the visitor.

## Egress probe (no credentials, no provisioning)

Run from a proposed host/network with IPv6 internet access:

```sh
bash specimens/ingress-tunnel/experiments/ipv6/egress.sh --probe-public-endpoints
```

The explicit flag permits eight public HTTPS probes (up to three redirects and
15 seconds per probe). The script forces IPv6, disables curl configuration and proxies,
verifies TLS, discards bodies and prints only fixed endpoint labels/statuses.
Exit 0 means HTTP transport worked for the sampled endpoints; exit 1 means at
least one transport failed; exit 2 means invalid invocation. HTTP 401/403/404 is
reachable, not usable authorization. Local failure can mean this laptop lacks
IPv6; it does not establish that GCP lacks it. This is excluded from CI/demo
network traffic. Never attach raw Terraform state, tokens, or operator DNS output
to the public PR.

The endpoint set follows `deploy/gcp/startup.sh`: Debian packages, Google artifact
and secret APIs, Cloud DNS, and ACME. The Caddy/server binaries are built locally,
so the VM does not need GitHub or Go module downloads. Actual image package
sources, redirects, DNS propagation checks, guest-agent updates and the full
production API permission set still require acceptance. The bounded live cold boot
passed package installation and authenticated artifact fetches; Let's Encrypt
worked but the ZeroSSL fallback endpoint failed. See the live report for limits.
Private Google Access can cover eligible Google APIs; it is not general internet
IPv4 translation. If a required non-Google endpoint lacks
IPv6, stop or design a bounded offline artifact/image path. **Do not add paid NAT.**

## Live acceptance and rollback plan

1. Review an isolated Terraform plan for a new experimental host: internal IPv4
   plus external IPv6, no external IPv4 access config, no NAT/router/LB, separate
   state and disk, IPv6 firewall rules, explicit costs and teardown ownership.
   Keep the existing pilot, address and DNS intact throughout this experiment.
2. Cold boot without external IPv4 and run the probe. Verify actual package
   installation, private artifact checksums, scoped secret reads, Caddy startup
   and certificate issuance/renewal. Reboot and replace the experimental VM,
   confirming retained claim/certificate state and stable origin addressing.
3. On only the explicitly selected test hostnames, verify authoritative DNS and
   proxied A/AAAA,
   browser certificate SANs, and origin TLS/SNI. `curl -4` to control health and
   a claimed host must pass from authorized home and work networks. Run the
   actual agent on an IPv4-only network, not only curl on a dual-stack machine.
4. Exercise the scenarios in the existing [live validation](../../deploy/gcp/LIVE-VALIDATION.md):
   signed synthetic callbacks within three seconds; stale/tampered rejections;
   exact 1 MiB body; streaming; public WSS; claim isolation; revoke/supersede;
   spoofed forwarding; loopback diagnostics; denied control source. Measure
   idle-link survival and reconnect after an interrupted proxy connection.
5. Review a sanitized evidence table and a bill projection. Only a later approved
   cutover may move real traffic and release the old IPv4 reservation. Detaching
   but retaining it does not eliminate its charge. Release is a separate action;
   the old address is not guaranteed recoverable afterward.

On any failure, keep traffic on the unchanged pilot, remove only experimental
DNS/resources through their isolated state, and retain or explicitly retire the
experimental state disk. Never run a broad destroy against the live deployment.
After any eventual IPv4 release, rollback needs a newly allocated address and DNS
update; budget the resulting outage rather than promising instant restoration.

## Cost and kill condition

External IPv6 addresses carry no address charge. The current in-use external
IPv4 rate is $0.005/hour: eliminating one saves at most **$43.80/year** at 8,760
hours. Traffic, compute, disk, DNS, secrets, domain renewal and experimental host
overlap still cost money. Free-tier VM/disk allowance is shared and conditional.
[GCP pricing](https://cloud.google.com/vpc/pricing),
[free tier](https://docs.cloud.google.com/free/docs/free-cloud-features#compute).

Reject any solution whose new annual certificate, proxy, NAT, domain or maintenance
cost exceeds that saving, or whose control access/identity semantics weaken.
A recurring add-on above $3.65/month alone erases the entire IPv4 saving. Obtain
an actual certificate quote; this PR does not assert an unverified add-on price.
The existing delegated names plus free parent certificate already fail the
hypothesis. The two-exact-host candidate has passed bounded live transport checks,
and the reusable pack subsequently passed live replacement and IPv4 release.
See the separate packaged-mode evidence; renewal, long-duration behavior and
measured billing remain unproven.
