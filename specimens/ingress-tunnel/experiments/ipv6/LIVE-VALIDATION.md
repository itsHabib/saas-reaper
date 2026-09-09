# Live IPv6 experiment evidence

A bounded GCP and Cloudflare experiment demonstrated this tunnel's public edge
and actual agent control link with **no external IPv4 on the experimental VM**.
This report records sanitized observations supplied by the agent conducting the
live run. It does not publish operator identifiers, credentials, or private
runtime files. The existing IPv4 pilot remained unchanged.

## Shape actually tested

- An isolated `e2-micro` in `us-central1-a`, with internal IPv4 and external IPv6,
  no public IPv4 access configuration, and no NAT or load balancer.
- Two explicitly named first-level hostnames on an existing registered domain:
  for example `control.example.com:8443` and `probe.example.com:443`.
  Their Cloudflare-proxied AAAA records pointed to the experimental IPv6 origin.
  No domain purchase, wildcard record, or delegated-zone migration was required.
- The tunnel server used `example.com` as its configured domain and one claim.
  Caddy obtained exact per-host Let's Encrypt certificates using HTTP-01;
  Cloudflare Full (strict) was verified. This tested neither wildcard issuance
  nor automatic issuance for arbitrary future claims.
- Origin ports 80, 443 and 8443 admitted only Cloudflare IPv6 ranges. Caddy
  restricted control requests by the Cloudflare visitor-IP header and rewrote
  the forwarded client address from that header. The server retained its existing
  loopback trust boundary. A service account could read only the private
  experimental artifact bucket; binaries were verified against SHA-256 hashes.

## Observed results

| Check | Observation | Boundary |
|---|---|---|
| Cold boot egress | Forced-IPv6 package index update and package installation passed | Required startup changes described below |
| Sampled endpoint transport | Debian and Google package endpoints, Google APIs and Let's Encrypt reachable over IPv6 | Unauthenticated API transport does not prove secret/DNS authorization |
| ACME fallback | ZeroSSL endpoint failed the IPv6 probe | Do not rely on automatic fallback; exact-host proof used Let's Encrypt |
| Private artifact download | Authenticated IPv6 fetch succeeded; binary SHA-256 matched | Artifact-only service account |
| Private Google Access | Google APIs reachable using private IPv4 | No public NAT; does not cover arbitrary internet IPv4 destinations |
| IPv4 agent path | Actual agent control connection and public WSS passed through a local CONNECT proxy forcing IPv4 connections to Cloudflare | Proves IPv4 transport; not a work-laptop firewall test |
| Exact request body | 1 MiB round trip matched in 1.735 seconds | One bounded sample |
| Synthetic signed callback | Valid callback returned 200 in 0.456 seconds | Fixture, not an actual Slack app |
| Callback rejection | Tampered and stale signatures returned 401 | Signature validation stays with the target |
| Streaming | First data at 0.241 seconds; completion at 1.455 seconds | Data arrived before full response completion |
| WebSocket | Public WSS passed | Not a long-duration idle soak |
| Read authority | Read response redacted credentials; read token could not write | Existing credential separation retained |
| Forwarding spoof | Supplied X-Forwarded-For discarded; spoofed Cloudflare visitor header returned 403 at Cloudflare | Does not prove authenticated origin pulls |
| Denied control source | A different external source received 403; spoofing the allowed visitor header also received 403 | Tested through Cloudflare |
| Telemetry | Loopback metrics reported one live link | Cloud Logging export permission was denied under the minimal experimental service account; no cloud log export claim |

## Startup issues found

The initial experimental startup could invoke curl before a global IPv6 address
was assigned. Curl selected a link-local address and hung. The live experiment's
private runtime was corrected to wait for a global address and bound connection
and retry times. A repeat bootstrap also required downloading binaries to a
`.next` file and atomically replacing the executable instead of overwriting a
running binary.

These fixes were made in the isolated experimental runtime, **not added as an
IPv6 mode to the supported Terraform pack**. A production patch must incorporate
and test the address-readiness behavior; the existing GCP startup already uses
`.next` binary replacement. Do not infer reproducible deployment automation from
this manually coordinated test.

## Reboot and cleanup

The post-reboot suite passed again: exact 1 MiB body in 1.747 seconds, valid
signed callback in 0.398 seconds, and streaming first data at 0.360 seconds with
completion at 1.561 seconds. All security and WSS assertions passed. Both
certificate file hashes were preserved, both services were active, and startup
completed successfully.

The test claim was revoked with HTTP 200 and the local agent, target and proxy
were stopped. The two temporary Cloudflare AAAA records were deleted; the four
original delegation NS records remained. The experimental VM and boot disk,
firewall rules, subnet and VPC, private artifact bucket and objects, service
account, and experiment-specific OS Login key were deleted. Follow-up resource
lists confirmed the VM, disk, firewall and subnet were absent.

The original pilot returned HTTP 200 from its health endpoint with strict TLS
after cleanup. Cloudflare Full (strict) was retained as the stronger TLS setting.
No current pilot IPv4 address has been released; the IPv4 bill has not disappeared.

## What remains unproven

- A packaged deployment, arbitrary wildcard claims, automated certificate renewal,
  authenticated origin pulls, and the complete trust behavior of a reusable pack.
- A real work-laptop network, real Slack app cutover, prolonged idle connections,
  sustained load, a measured bill, or recurring maintenance cost.
- VM replacement/state retention for this IPv6 configuration, cross-zone recovery,
  migration of the existing pilot, or any live AWS deployment.
- Authenticated Secret Manager and DNS mutation through the intended production
  service account. The artifact-only live proof does not establish those permissions.

The unchanged deep delegated-domain hypothesis remains false. The narrower
**two exact first-level hostnames** experiment passed the transport checks above
without a new domain purchase or a paid IPv4/NAT dependency. Further packaging
and acceptance work is needed before releasing an existing address.
