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

These initial fixes were made in the isolated experimental runtime. The later
packaged mode incorporates address readiness and bounded downloads; the existing
GCP startup already uses `.next` binary replacement. The initial observations
alone did not establish reproducible deployment automation.

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

## Boundary after the initial experiment

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


## Packaged mode validation

The coordinating agent subsequently deployed implementation `83fac0e` through the
opt-in Terraform mode against the retained pilot state disk, keeping the old IPv4
reservation detached for rollback. This is distinct from the removed initial
experimental host. Live operations were separately authorized; the implementation
worker did not read private state or perform cloud/DNS mutations.

| Packaged check | Observed result |
|---|---|
| Synthetic signed callback | 200 in 0.274 seconds; 0.414 seconds after reboot |
| Exact 1 MiB body | Passed in 1.672 seconds; 1.747 seconds after reboot |
| Streaming | First data 0.229 seconds / completion 1.437 seconds; post-reboot 0.491 / 1.713 seconds |
| Protocol/security suite | WSS, stale/tampered callback rejection and forwarding checks passed |
| Origin authentication | Cloudflare origin-header rule disabled: 403; re-enabled: 200 |
| Cache policy | Dynamic cacheable target response and spoof checks passed with Cloudflare bypass active |
| Visitor restrictions | IPv6 visitor edge: 200; denied and forged control requests: 403 |
| DNS authority | Host DNS operation rejected with 403 |
| Reboot state | Five certificate files, including retained previous certificates and new exact-host certificates, preserved |

Observed agent reconnects coincided with the client machine sleeping. The next
awake observation lasted more than 213 seconds without a link loss. Read-only
inspection found no post-handshake HTTP-client deadline; this is a bounded
observation, not a guarantee against network loss or a long-duration soak.

Live validation found an origin-header redaction gap in Caddy's default access
logger, despite the named access-log filters. Commit `0c73702` adds a global
filter and removes the header from the original authenticated request before
proxying. Its real-Caddy proof covers explicit-port and unmatched hosts, rejected
requests and backend 502 responses and checks all logs for fixture secrets.
The fix at `0c73702` was then deployed through an actual VM replacement and
origin-secret rotation. The same normalized static IPv6 address, five certificate
file hashes, existing probe claim and its agent credential survived replacement.
No external IPv4 was attached. The full suite passed again: 1 MiB in 1.884
seconds, signed callback in 0.207 seconds, streaming first data at 0.205 seconds
and completion at 1.410 seconds, plus WSS, cache and header checks.

After deliberate offline 502 and unmatched-listener requests, the coordinating
agent checked all live Caddy journal entries and confirmed the current origin
secret was absent. The Caddy admin API was disabled. The prior origin-secret
version was destroyed and its replacement enabled, with the Cloudflare rule
updated to match. This verifies the fix and rotation without publishing secret
values or logs. Terraform then returned no drift (exit 0). A separately reviewed
release plan deleted only the old IPv4 reservation; its apply completed
successfully. Post-release health returned HTTP 200 and the address inventory
contained only the static external IPv6 reservation. The old IPv4 hourly charge
has therefore been removed from this deployment; a measured bill is still pending.
The post-release full suite passed again: 1 MiB in 1.729 seconds, signed callback
in 0.368 seconds, streaming first data at 0.337 seconds and completion at 1.547
seconds, with all transport, security and cache assertions passing.

Final cleanup was confirmed: the synthetic probe claim was revoked and the agent
exited on revocation; local target/proxy fixtures stopped. The temporary IAP
firewall and experiment-specific OS Login key were removed. The former Cloud DNS
parent zone, verified empty except for NS/SOA, was deleted. Precisely its four
retired parent delegation NS records were removed from Cloudflare, leaving the
three new proxied AAAA records. The pre-migration state snapshot was retained
intentionally for recovery; snapshot/storage cost is not zeroed by IPv4 release.

Remaining acceptance includes certificate renewal, sustained load, real Slack
and work-network use, measured billing and cross-zone recovery. Exact-host mode
does not add arbitrary wildcard claim publication or automatic failover.
