# Opt-in IPv6 origin behind Cloudflare

`network_mode = "cloudflare_ipv6"` packages the bounded [live experiment](../../experiments/ipv6/LIVE-VALIDATION.md).
The default remains the existing direct IPv4 deployment. The mode exposes a finite
list of exact first-level hostnames through Cloudflare; it does not automatically
publish arbitrary claims. The initial live experiment predates this reusable
configuration: validate this pack's rendered plan and actual startup separately.

## Inputs and authority

Use a registered domain already active in Cloudflare full setup, such as
`example.com`. Control is `control.example.com:8443`; named claims are
`probe.example.com` and `slack.example.com`. These names fit the parent Universal
SSL coverage. Nested delegated names such as `slack.tunnel.example.com` do not.
No purchase or authority migration is implied.

Example private Terraform variables:

```hcl
network_mode = "cloudflare_ipv6"
project_id = "your-project"
domain = "example.com"
control_name = "control"
claim_names = ["probe", "slack"]
control_cidrs = ["203.0.113.7/32"]
acme_email = "you@example.com"
# During migration only: retain the billable old IPv4 for rollback.
retain_ipv4_reservation = true
```

The mode accepts IPv4 or IPv6 visitor CIDRs. Omitted `edge_cidrs` allows both
families; an explicit list restricts them. Control always requires a nonempty
allowlist. These are visitor restrictions enforced by Caddy, not GCP source
rules: the VM accepts only Cloudflare's published IPv6 ranges on 80/443/8443.
The range snapshot is explicit in Terraform and must be maintained from
[Cloudflare's list](https://www.cloudflare.com/ips-v6/).

The VM has internal IPv4, a separately reserved static external IPv6 range,
Private Google Access, no external IPv4 interface and no NAT/load balancer.
Its retained state disk and admin/read Secret Manager credentials remain under
the existing ownership rules. A third independent secret authenticates the
origin. The VM receives no Cloudflare credential and no Cloud DNS write grant
in this mode. Terraform state and sensitive outputs must remain private.

## Configure Cloudflare after reviewing the plan

The core pack deliberately does not own an existing zone-wide Cloudflare ruleset.
An operator with DNS-edit and request-header-rule permissions performs these steps
in the existing zone; no Cloudflare provider/token is required by Terraform.

1. Review `terraform plan`: retain the exact state-disk identity, preserve the old
   IPv4 reservation during migration, and require no public IPv4 attachment or NAT.
   Switching modes removes this pack's old child Cloud DNS zone, delegation and A
   records and its host DNS IAM. Review rollback implications before applying;
   unrelated registrar/parent-zone records are not managed by the mode.
2. After an approved apply, use `terraform output -json cloudflare_dns_records` to
   create only the listed **proxied AAAA** records. DNS-only records cannot serve
   IPv4-only visitors. Do not overwrite unrelated records or a delegated subtree.
3. Create a request-header transform rule using the exact expression from
   `cloudflare_origin_rule_expression`. **Set static** `X-Reaper-Origin` to the
   sensitive `cloudflare_origin_header_value`; overwrite any supplied value.
   Never append or preserve a caller's header. Do not put the secret in a URL,
   public issue, shell tracing, or access log. Missing/wrong secret fails closed.
4. Use Full (strict), with caching bypass for these exact hostnames. Ensure HTTP-01
   requests are not challenged or redirected away, and permit WebSockets on the
   control and edge hosts. Caddy obtains per-host Let's Encrypt certificates via
   port 80; only that ACME listener bypasses the origin header requirement.
   Caddy uses no ZeroSSL fallback. Port 80 does not forward application traffic.
5. Verify strict-TLS health from an allowed client and create only a listed claim
   using the management API. Run the real agent against `control_url`. An API
   claim outside `claim_names` does not create DNS or an exposed Caddy route.

Caddy trusts the visitor header only from the configured Cloudflare peer ranges,
requires the parsed client IP to equal the header, then applies visitor CIDRs.
The independent origin secret prevents another Cloudflare tenant from passing
this check merely by reaching the origin. It is removed before forwarding and
from access logs, including rejected requests. Caddy's admin API is disabled.
Rotate the origin secret with a coordinated Cloudflare rule update and Caddy
restart; a mismatch intentionally causes 403 during the transition.

Caddy forces `Cache-Control: no-store` after the target response, including when
the target advertises public caching. Preserve the explicit Cloudflare cache
bypass rule: a zone rule that overrides origin cache control can still break
transparent tunnel semantics. Disable response transformations that alter bodies.

## Validate before releasing IPv4

The local deployment proof executes the actual pinned Caddy policy on loopback:
valid traffic, wrong/missing origin secrets, denied/malformed/combined visitor
headers, IPv6 public visitors, explicit IPv4-only control, forwarded identity,
secret stripping and log redaction, and a backend advertising cacheable `.js`
responses overridden to no-store. Terraform mocks verify both deployment modes
and migration reservation retention. They do not contact Cloudflare.

On the actual pack, verify IPv6 readiness and bounded downloads on cold boot,
private artifacts/secrets, HTTP-01 issuance, origin rejection, no cache reuse on
a changing `.js` body through Cloudflare, IPv4-only agent attachment, public WSS,
streaming, signed fixtures and denied sources. Reboot and replace the VM to check
static IPv6 and retained claims/certificates. Keep raw operator evidence private.
Follow the remaining [acceptance plan](../../experiments/ipv6/README.md).

Only after those checks should a separately reviewed apply set
`retain_ipv4_reservation = false`. Keeping a detached reservation is still
billable. Releasing it forfeits guaranteed reuse of that address. Switching back
to direct mode requires valid parent Cloud DNS inputs and a reviewed plan to
restore its delegation, firewall and certificates; account for downtime.

No claim here promises wildcard automation, real Slack acceptance, work-network
reachability, certificate renewal, measured savings, long-term availability or
cross-zone recovery. This remains a single VM with retained state.
