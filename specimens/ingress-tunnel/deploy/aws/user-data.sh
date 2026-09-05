#!/usr/bin/env bash
# Terraform renders every single-quoted placeholder before this script runs.
# shellcheck disable=SC2016
set -euo pipefail

region='${region}'
bucket='${artifact_bucket}'
server_key='${server_key}'
caddy_key='${caddy_key}'
state_volume='/dev/disk/by-id/nvme-Amazon_Elastic_Block_Store_vol${state_volume_id}'
lib=/usr/local/lib/reaper-tunnel

install -d -m 0755 /etc/caddy
install -d -m 0750 -o 65532 -g 65532 /var/lib/caddy
install -d -m 0755 "$lib"

# The claims database lives on the separate state volume, which outlives this instance.
for _ in $(seq 1 120); do
  if [[ -e "$state_volume" ]]; then
    break
  fi
  sleep 1
done
if [[ ! -e "$state_volume" ]]; then
  echo "state volume $state_volume never appeared" >&2
  exit 1
fi
if ! blkid "$state_volume" > /dev/null 2>&1; then
  mkfs.ext4 -q -L reaper-state "$state_volume"
fi
install -d -m 0750 /var/lib/reaper-tunnel
printf '%s /var/lib/reaper-tunnel ext4 defaults,nofail 0 2\n' "$state_volume" >> /etc/fstab
mount /var/lib/reaper-tunnel
chown 65532:65532 /var/lib/reaper-tunnel
chmod 0750 /var/lib/reaper-tunnel

# Every value the host needs at runtime is written once here, for the scripts below to read;
# the scripts themselves contain no Terraform placeholders and no systemd-visible variables.
install -m 0644 /dev/null /etc/reaper-tunnel-host.conf
{
  printf 'region=%s\n' "$region"
  printf 'bucket=%s\n' "$bucket"
  printf 'server_key=%s\n' "$server_key"
  printf 'caddy_key=%s\n' "$caddy_key"
  printf 'domain=%s\n' '${domain}'
  printf 'admin_actor=%s\n' "$(printf '%s' '${admin_actor_base64}' | base64 -d)"
  printf 'admin_token_secret_id=%s\n' '${admin_token_secret_id}'
  printf 'read_token_secret_id=%s\n' '${read_token_secret_id}'
} >> /etc/reaper-tunnel-host.conf

# render-env reads both tokens from Secrets Manager and writes the service's environment file.
# It runs before every service start, so a rotated secret takes effect on the next restart.
cat > "$lib/render-env.sh" << 'RENDER'
#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=/dev/null
source /etc/reaper-tunnel-host.conf

secret() {
  aws --region "$region" secretsmanager get-secret-value \
    --secret-id "$1" --query SecretString --output text
}

install -m 0600 -o 65532 -g 65532 /dev/null /etc/reaper-tunnel.env.next
{
  printf 'REAPER_TUNNEL_CONTROL_ADDR=127.0.0.1:8081\n'
  printf 'REAPER_TUNNEL_EDGE_ADDR=127.0.0.1:8080\n'
  printf 'REAPER_TUNNEL_DOMAIN=%s\n' "$domain"
  printf 'REAPER_TUNNEL_DB=/var/lib/reaper-tunnel/tunnel.db\n'
  printf 'REAPER_TUNNEL_FORWARD_PROTO=https\n'
  printf 'REAPER_TUNNEL_ADMIN_ACTOR=%s\n' "$admin_actor"
  printf 'REAPER_TUNNEL_ADMIN_TOKEN=%s\n' "$(secret "$admin_token_secret_id")"
  printf 'REAPER_TUNNEL_READ_TOKEN=%s\n' "$(secret "$read_token_secret_id")"
} >> /etc/reaper-tunnel.env.next
mv /etc/reaper-tunnel.env.next /etc/reaper-tunnel.env
RENDER
chmod 0755 "$lib/render-env.sh"

# fetch-binaries installs both pinned, apply-built binaries from the private bucket and prints
# the name of every service whose binary changed. The updater timer restarts exactly those.
cat > "$lib/fetch-binaries.sh" << 'FETCH'
#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=/dev/null
source /etc/reaper-tunnel-host.conf
changed=''

install_object() {
  local key=$1
  local target=$2
  local marker="/var/lib/reaper-tunnel/.etag-$key"
  local etag
  etag=$(aws --region "$region" s3api head-object --bucket "$bucket" --key "$key" --query ETag --output text)
  if [[ -f "$marker" && "$(cat "$marker")" == "$etag" && -x "$target" ]]; then
    return 1
  fi
  aws --region "$region" s3 cp "s3://$bucket/$key" "$target.next" --quiet
  chmod 0755 "$target.next"
  mv "$target.next" "$target"
  printf '%s\n' "$etag" > "$marker"
  return 0
}

if install_object "$server_key" /usr/local/bin/reaper-tunnel; then
  changed="$changed reaper-tunnel"
fi
if install_object "$caddy_key" /usr/local/bin/caddy; then
  setcap cap_net_bind_service=+ep /usr/local/bin/caddy
  changed="$changed caddy"
fi
printf '%s\n' "$changed"
FETCH
chmod 0755 "$lib/fetch-binaries.sh"

cat > "$lib/update.sh" << 'UPDATE'
#!/usr/bin/env bash
set -euo pipefail
for unit in $(/usr/local/lib/reaper-tunnel/fetch-binaries.sh); do
  systemctl restart "$unit.service"
done
UPDATE
chmod 0755 "$lib/update.sh"

"$lib/fetch-binaries.sh" > /dev/null
"$lib/render-env.sh"

# Caddy terminates TLS with one wildcard certificate obtained through Route 53 DNS-01 and
# renews it on its own. The apex on 8443 is the control plane; every wildcard host on 443 is
# the public edge. Nothing listens on 80.
cat > /etc/caddy/Caddyfile << 'CADDY'
{
	email ${acme_email}
	auto_https disable_redirects
}

${domain}:8443 {
	tls {
		dns route53
	}
	reverse_proxy 127.0.0.1:8081
}

*.${domain}:443 {
	tls {
		dns route53
	}
	reverse_proxy 127.0.0.1:8080
}
CADDY

# The + prefix runs the pre-start step with full privileges so it can read the secrets and
# write /etc while the service itself stays unprivileged.
cat > /etc/systemd/system/reaper-tunnel.service << 'UNIT'
[Unit]
Description=Reaper ingress tunnel server
After=network-online.target
Wants=network-online.target
RequiresMountsFor=/var/lib/reaper-tunnel

[Service]
User=65532
Group=65532
ExecStartPre=+/usr/local/lib/reaper-tunnel/render-env.sh
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

# A rebuilt binary reaches the host within five minutes of the apply, without replacing the
# instance: the timer compares object ETags and restarts only what changed.
cat > /etc/systemd/system/reaper-tunnel-update.service << 'UNIT'
[Unit]
Description=Install rebuilt reaper tunnel binaries from the artifact bucket

[Service]
Type=oneshot
ExecStart=/usr/local/lib/reaper-tunnel/update.sh
UNIT

cat > /etc/systemd/system/reaper-tunnel-update.timer << 'UNIT'
[Unit]
Description=Check the artifact bucket for rebuilt reaper tunnel binaries

[Timer]
OnBootSec=5min
OnUnitActiveSec=5min

[Install]
WantedBy=timers.target
UNIT

systemctl daemon-reload
systemctl enable --now reaper-tunnel.service
systemctl enable --now caddy.service
systemctl enable --now reaper-tunnel-update.timer
