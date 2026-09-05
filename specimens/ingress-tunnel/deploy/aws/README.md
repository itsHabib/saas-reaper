# AWS deployment pack

One `terraform apply` turns the ingress-tunnel specimen into a company tunnel host: a small
instance behind an Elastic IP, apex and wildcard DNS records in your Route 53 zone, and a
wildcard certificate that Caddy obtains through DNS-01 and renews on its own. The server binary
is cross-compiled on the machine running Terraform and shipped through a private bucket, so the
instance never compiles anything and never contacts a registry.

What you bring:

- a Route 53 hosted zone and a domain inside it, such as `tunnel.example.com`;
- two Secrets Manager secrets holding the management token and the read token;
- Go and Terraform on the machine that runs the apply.

```sh
cd specimens/ingress-tunnel/deploy/aws
terraform init
terraform apply \
  -var region=us-east-1 \
  -var domain=tunnel.example.com \
  -var hosted_zone_id=Z0123456789ABCDEFGHIJ \
  -var acme_email=ops@example.com \
  -var admin_actor=platform-team \
  -var admin_token_secret_arn=arn:aws:secretsmanager:...:secret:reaper-tunnel-admin \
  -var read_token_secret_arn=arn:aws:secretsmanager:...:secret:reaper-tunnel-read
```

The outputs print the control URL, the edge pattern, and one-line claim and agent examples.
Give a developer one credential:

```sh
curl -X POST https://tunnel.example.com/v1/tunnels \
  -H "Authorization: Bearer $ADMIN_TOKEN" \
  -H 'Content-Type: application/json' \
  -d '{"subdomain":"acme"}'
```

They run the agent against whatever they are building:

```sh
REAPER_TUNNEL_AGENT_SERVER=https://tunnel.example.com \
REAPER_TUNNEL_AGENT_TOKEN=rtk_... \
REAPER_TUNNEL_AGENT_TARGET=http://127.0.0.1:3000 \
reaper-tunnel-agent
```

`https://acme.tunnel.example.com` now reaches their port 3000. Revoking the claim closes the
link immediately and the token never works again.

## Shape

- Caddy listens on 443. The apex host proxies to the control plane on `127.0.0.1:8081`; every
  wildcard host proxies to the public edge on `127.0.0.1:8080`. Both Go listeners bind loopback
  only, so the security group opens 80 and 443 and nothing else.
- Route 53 records are `A` records to the Elastic IP with a 60-second TTL.
- The instance role can read the two secrets, fetch the one server object, and change records
  in the one hosted zone. It can do nothing else.
- IMDSv2 is required and the root volume is encrypted.
- `terraform apply` rebuilds and re-ships the binary whenever the specimen's Go source changes,
  and replaces the instance when its user data changes.

## What the pack does not do

- It does not put a company identity provider in front of visitor traffic. The edge serves
  whoever can resolve the host; a claimed subdomain is as public as the developer's port would be
  on the internet. Visitor authentication is the natural next addition and belongs at the edge.
- It does not run more than one host. One tunnel server is a single process by design; the
  registry of live links is in memory and the claims are in one SQLite file.
- It does not manage the two secrets. Create them before the apply and rotate them by editing
  the secrets and restarting the service.

Validate the pack without an account:

```sh
make -C specimens/ingress-tunnel deploy-check
```
