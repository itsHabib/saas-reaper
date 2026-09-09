# GCP tunnel host

One apply builds and installs the tunnel server and a pinned Caddy with Cloud DNS support,
creates one E2 VM with a static IPv4 address, creates and delegates a dedicated tunnel DNS
subzone, writes apex and wildcard DNS, and generates two
API credentials in Secret Manager. Caddy obtains and renews the certificates through DNS-01.
The host uses its service account; no service-account key files are created.

Bring a billing-enabled project, a **publicly delegated Cloud DNS zone in that project**,
a subdomain beneath it, Go 1.26.5 (the tested builder), Terraform 1.15.8 or newer, and Google Cloud CLI credentials with permission to
create the listed resources and IAM bindings. A dedicated project is easiest to reason about.
Your existing parent zone must already be delegated at the registrar. Terraform creates
the child zone and its parent NS records; no additional registrar work is required. The host
gets DNS mutation access only to that dedicated child zone, never your parent-zone records.

```sh
gcloud auth application-default login
cd specimens/ingress-tunnel/deploy/gcp
terraform init
terraform apply \
  -var project_id=your-project \
  -var domain=tunnel.example.com \
  -var dns_zone=example-com \
  -var acme_email=you@example.com \
  -var 'control_cidrs=["203.0.113.7/32"]'
```

Replace the example address with your public IP. The control plane on 8443 requires an
explicit allowlist. The visitor edge on 443 is public by default, which Slack needs.
No SSH, diagnostics, pprof, HTTP port 80, load balancer, or NAT gateway is exposed.
The default is `e2-micro` in `us-central1-a`, with 10 GiB boot and 10 GiB state disks,
both `pd-standard`. API enablement is included; billing activation is not.

An apply finishing does **not** mean bootstrap or TLS succeeded. Inspect the serial output:

```sh
$(terraform output -raw startup_log_command)
curl --fail "$(terraform output -raw control_url)/healthz"
```

The health request must originate within `control_cidrs`. Allow time for IAM propagation,
package installation, DNS propagation, and certificate issuance. Startup retries API requests;
a persistent bootstrap error remains visible in serial output. A live deployment has
now verified startup, DNS/TLS, scoped resource access,
and public tunnel traffic. See [live validation](LIVE-VALIDATION.md) for the exact scope and
remaining gaps. Local proof still validates Terraform, mocked transitions, startup syntax,
the pinned Caddy adapter, and cross-compilation separately.

## One claim and one local command

Build the agent on your Mac from the specimen directory:

```sh
go build -o reaper-tunnel-agent ./cmd/reaper-tunnel-agent
```

From `deploy/gcp`, create the claim with the management token. This returns the one-time
agent token; store it locally, not in Terraform variables or Git:

```sh
curl --fail-with-body "$(terraform output -raw control_url)/v1/tunnels" \
  -H "Authorization: Bearer $(terraform output -raw admin_token)" \
  -H 'Content-Type: application/json' -d '{"subdomain":"slack"}'
```

Run the agent with that token, pointing at the local escalation listener:

```sh
REAPER_TUNNEL_AGENT_SERVER=https://tunnel.example.com:8443 \
REAPER_TUNNEL_AGENT_TOKEN=rtk_REPLACE_WITH_ISSUED_TOKEN \
REAPER_TUNNEL_AGENT_TARGET=http://127.0.0.1:YOUR_PORT \
./reaper-tunnel-agent
```

The callback URL is `https://slack.tunnel.example.com/<existing-callback-path>`.
The app behind the tunnel still owns Slack signature verification and callback authorization.
See [the Slack pilot checklist](SLACK-PILOT.md) before switching the real app.

## Ownership and updates

- The retained disk holds separate tunnel and Caddy directories, owned by UIDs 65532 and
  65531. TLS storage survives replacement as well as claims and audit. `prevent_destroy`
  blocks accidental deletion, including ordinary `terraform destroy`. Snapshot and make an
  explicit retirement decision before removing this protection. Cross-zone changes are refused.
- Code or bootstrap changes replace the VM deliberately. Expect a short outage and agent
  reconnection; this is a single host, not a zero-downtime deployment. The static IP and state
  disk remain. The boot image does not drift automatically on later plans; request an instance
  replacement explicitly for OS refresh. Do not change the zone without a snapshot migration.
- Both credentials are fetched at each service start. Rotate via Terraform and restart or
  replace the VM to activate the new values. Terraform state contains secrets; use an
  access-controlled remote backend for ongoing operation. Local state and variable files are ignored.
- The host can read only this artifact bucket and these two secrets. DNS mutations are scoped
  to the dedicated child zone, with project-wide zone-list permission for discovery. Both processes
  still share that host service account via metadata; different UIDs are filesystem separation,
  not complete isolation against arbitrary code execution in Caddy.
- Structured server and Caddy logs go to journald, bounded at 100 MiB on the boot disk. They
  do not survive replacement. Prometheus remains at loopback port 8082; pprof stays off.
  This minimal pack has no paid cloud log/metric exporter and no automatic backup schedule.
- The host installs Debian packages at boot. Server, Caddy, xcaddy and the DNS module are
  built locally with explicit pins; OS packages and certificate issuance require network access.

For the bounded investigation into removing the IPv4 charge, see the
[IPv6 feasibility experiment](../../experiments/ipv6/README.md). It is not a supported deployment mode.

## Cost target

Estimate checked on 2026-09-07, in USD. If the billing account has the free-tier E2 VM and
standard-disk allowance available, the recurring baseline is about **$46/year**:
`$0.005 × 8,760 hours` for IPv4 plus `$0.20 × 12` for one DNS zone. This excludes domain
registration, queries, outbound traffic, artifact storage/operations, and Secret Manager usage.
The new dedicated zone adds $0.20/month; existing parent-zone costs are excluded. Free-tier eligibility and usage
are shared across the billing account; without available allowance, compute and disks add cost.
This is a planning estimate, not a measured bill or a promise to beat ngrok.

Sources: [free-tier eligibility](https://docs.cloud.google.com/free/docs/free-cloud-features#compute),
[IPv4 and network prices](https://cloud.google.com/vpc/network-pricing),
[DNS prices](https://cloud.google.com/dns/pricing).

Validate without a cloud account or provisioning resources:

```sh
make -C specimens/ingress-tunnel deploy-check
```

The tunnel CI uses Terraform 1.15.8, matching the tested local runtime. Terraform 1.8.5
passes the mocked plan assertions but fails test teardown on `prevent_destroy`; the disk
protection remains enabled, and older Terraform versions are refused by this pack.

The full Caddy build also requires a newer Go toolchain than the root module minimum.
The tunnel CI pins Go 1.26.5; Go 1.25.0 refuses the pinned Caddy release.
