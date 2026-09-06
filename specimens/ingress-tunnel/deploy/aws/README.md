# AWS deployment pack

One `terraform apply` turns the ingress-tunnel specimen into a company tunnel host: a small
instance behind an Elastic IP, apex and wildcard DNS records in your Route 53 zone, a
wildcard certificate that Caddy obtains through DNS-01 and renews on its own, two API tokens
minted by the apply, and a separate encrypted volume for the claims database that outlives the
instance. Both binaries the host runs, the tunnel server and a Caddy pinned to an exact release
with the Route 53 module built in, are compiled on the machine running Terraform and shipped
through a private bucket. The instance never compiles anything and never contacts a registry
or a vendor's download service.

What you bring:

- an AWS account, and a Route 53 hosted zone that contains the domain you want, such as
  `tunnel.example.com`;
- Go and Terraform on the machine that runs the apply.

```sh
cd specimens/ingress-tunnel/deploy/aws
terraform init
terraform apply \
  -var region=us-east-1 \
  -var domain=tunnel.example.com \
  -var hosted_zone_id=Z0123456789ABCDEFGHIJ \
  -var acme_email=ops@example.com \
  -var 'control_cidrs=["203.0.113.7/32"]'
```

The first apply builds Caddy from source with `xcaddy`, which takes a minute or two; later
applies rebuild only what changed. The outputs print the control URL, the edge pattern, and
one-line claim and agent examples. The tokens are sensitive outputs:

```sh
terraform output -raw admin_token
terraform output -raw read_token
```

Give a developer one credential:

```sh
curl -X POST https://tunnel.example.com:8443/v1/tunnels \
  -H "Authorization: Bearer $(terraform output -raw admin_token)" \
  -H 'Content-Type: application/json' \
  -d '{"subdomain":"acme"}'
```

They run the agent against whatever they are building:

```sh
REAPER_TUNNEL_AGENT_SERVER=https://tunnel.example.com:8443 \
REAPER_TUNNEL_AGENT_TOKEN=rtk_... \
REAPER_TUNNEL_AGENT_TARGET=http://127.0.0.1:3000 \
reaper-tunnel-agent
```

`https://acme.tunnel.example.com` now reaches their port 3000. Revoking the claim closes the
link immediately and the token never works again.

## Who may reach it

Two allowlists, both optional, both enforced by the security group before a packet reaches
the host:

- `control_cidrs` is your machines: whoever may attach an agent or call the management and
  read APIs on port 8443. Register an office range, a VPN range, or each developer's address.
  Empty means anywhere.
- `edge_cidrs` is who may reach the tunnels themselves on port 443. Empty means the internet,
  which is what a webhook provider or a demo needs; narrow it to your own address while
  testing.

Your current address, for either list:

```sh
echo "$(curl -s https://checkip.amazonaws.com)/32"
```

The control plane is on its own port so the two lists can differ. The typical company shape
is control locked to the office and edge open; the typical solo shape is both locked to one
address until something needs to be public.

## Shape

- Caddy listens on 8443 and 443. The apex on 8443 proxies to the control plane on
  `127.0.0.1:8081`; every wildcard host on 443 proxies to the public edge on `127.0.0.1:8080`.
  Both Go listeners bind loopback only. Nothing listens on 80; certificates use DNS
  validation, so no HTTP challenge is needed.
- Route 53 records are `A` records to the Elastic IP with a 60-second TTL.
- The instance role can read the two secrets, fetch the two binaries, and change records in
  the one hosted zone. It can do nothing else.
- IMDSv2 is required and both volumes are encrypted.
- The claims database is on a 1 GiB `gp3` volume attached at `/var/lib/reaper-tunnel`. The
  instance is disposable; the volume is not. `terraform apply -replace=aws_instance.tunnel`
  brings up a fresh host on the same data.
- A changed Go source rebuilds and re-ships the server on the next apply; a changed pin
  rebuilds Caddy. A timer on the host checks the bucket every five minutes and restarts only
  the service whose binary changed. The instance is never replaced by a code change, and a
  newer AMI never replaces it on its own.
- The service reads both tokens from Secrets Manager before every start, so tokens rotate
  with `terraform apply -replace=random_password.admin_token` followed by
  `systemctl restart reaper-tunnel` on the host.
- Changing `domain`, `acme_email`, or `admin_actor` replaces the instance on purpose; the
  state volume and its claims survive. Every other input change leaves the host alone.

## What you can see

- `/<name>/server` in CloudWatch Logs carries the tunnel server's structured log, including
  one access line per proxied request.
- `/<name>/caddy` carries Caddy's JSON access logs for the edge and the control port: the
  outermost view, with the real client address, TLS details, and hosts the Go edge never saw.
- The `<name>` metrics namespace carries every `reaper_tunnel_*` series, by subdomain, scraped
  once a minute from the loopback diagnostics port.
- `-var pprof=true` opens the Go profiler on that loopback port for a session on the host
  (`ssh` or SSM, then `go tool pprof http://127.0.0.1:8082/debug/pprof/profile`). It changes
  the host configuration, so the apply replaces the instance; the state volume survives.

## What the pack does not do

- It does not put a company identity provider in front of visitor traffic. Within
  `edge_cidrs`, a claimed subdomain is as public as the developer's port would be. Visitor
  authentication is the natural next addition and belongs at the edge; per-claim source
  addresses in the server itself are the step before it.
- It does not run more than one host. One tunnel server is a single process by design; the
  registry of live links is in memory and the claims are in one SQLite file.
- `terraform destroy` deletes the state volume and with it every claim and audit row. Snapshot
  it first if that history matters.

Validate the pack without an account:

```sh
make -C specimens/ingress-tunnel deploy-check
```
