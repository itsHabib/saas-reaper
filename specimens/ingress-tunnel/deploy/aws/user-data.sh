#!/usr/bin/env bash
# Terraform renders every single-quoted placeholder before this script runs.
# shellcheck disable=SC2016
set -euo pipefail

install -d -m 0750 -o 65532 -g 65532 /var/lib/reaper-tunnel
install -d -m 0755 /etc/caddy
install -d -m 0750 -o 65532 -g 65532 /var/lib/caddy

secret() {
  aws --region '${region}' secretsmanager get-secret-value \
    --secret-id "$1" --query SecretString --output text
}

# The server binary was cross-compiled by the apply and staged in a private bucket.
aws --region '${region}' s3 cp 's3://${artifact_bucket}/${artifact_key}' /usr/local/bin/reaper-tunnel
chmod 0755 /usr/local/bin/reaper-tunnel
printf '%s\n' '${artifact_digest}' > /var/lib/reaper-tunnel/source-digest

# Caddy terminates TLS with a wildcard certificate obtained through Route 53 DNS-01 and
# renews it on its own. The route53 module needs no credentials beyond the instance role.
curl --fail --silent --show-error --location \
  'https://caddyserver.com/api/download?os=linux&arch=${architecture}&p=github.com/caddy-dns/route53' \
  --output /usr/local/bin/caddy
chmod 0755 /usr/local/bin/caddy
setcap cap_net_bind_service=+ep /usr/local/bin/caddy

install -m 0600 /dev/null /etc/reaper-tunnel.env
{
  printf 'REAPER_TUNNEL_CONTROL_ADDR=127.0.0.1:8081\n'
  printf 'REAPER_TUNNEL_EDGE_ADDR=127.0.0.1:8080\n'
  printf 'REAPER_TUNNEL_DOMAIN=%s\n' '${domain}'
  printf 'REAPER_TUNNEL_DB=/var/lib/reaper-tunnel/tunnel.db\n'
  printf 'REAPER_TUNNEL_FORWARD_PROTO=https\n'
  printf 'REAPER_TUNNEL_ADMIN_ACTOR=%s\n' "$(printf '%s' '${admin_actor_base64}' | base64 -d)"
  printf 'REAPER_TUNNEL_ADMIN_TOKEN=%s\n' "$(secret '${admin_token_secret_arn}')"
  printf 'REAPER_TUNNEL_READ_TOKEN=%s\n' "$(secret '${read_token_secret_arn}')"
} >> /etc/reaper-tunnel.env
chown 65532:65532 /etc/reaper-tunnel.env

cat > /etc/caddy/Caddyfile << 'CADDY'
{
	email ${acme_email}
}

${domain} {
	tls {
		dns route53
	}
	reverse_proxy 127.0.0.1:8081
}

*.${domain} {
	tls {
		dns route53
	}
	reverse_proxy 127.0.0.1:8080
}
CADDY

cat > /etc/systemd/system/reaper-tunnel.service << 'UNIT'
[Unit]
Description=Reaper ingress tunnel server
After=network-online.target
Wants=network-online.target

[Service]
User=65532
Group=65532
EnvironmentFile=/etc/reaper-tunnel.env
ExecStart=/usr/local/bin/reaper-tunnel
Restart=always
RestartSec=2
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/reaper-tunnel
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT

cat > /etc/systemd/system/caddy.service << 'UNIT'
[Unit]
Description=Caddy TLS edge for the reaper tunnel
After=network-online.target reaper-tunnel.service
Wants=network-online.target

[Service]
User=65532
Group=65532
Environment=XDG_DATA_HOME=/var/lib
Environment=XDG_CONFIG_HOME=/var/lib
ExecStart=/usr/local/bin/caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
ExecReload=/usr/local/bin/caddy reload --config /etc/caddy/Caddyfile --adapter caddyfile --force
Restart=always
RestartSec=5
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/caddy
PrivateTmp=true

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable --now reaper-tunnel.service
systemctl enable --now caddy.service
