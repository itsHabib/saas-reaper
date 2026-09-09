#!/usr/bin/env bash
# Terraform renders the placeholders. No credentials are embedded in instance metadata.
# shellcheck disable=SC2016,SC2154
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive
apt-get update
apt-get install -y ca-certificates curl jq passwd

# systemd resolves service credentials through NSS even when User= is numeric.
# Reserve both identities explicitly and refuse collisions on repeat boots.
service_identity() {
  local name=$1 number=$2
  if ! getent group "$name" > /dev/null; then
    groupadd --gid "$number" "$name"
  fi
  [[ "$(getent group "$name" | cut -d: -f3)" == "$number" ]]
  if ! id "$name" > /dev/null 2>&1; then
    useradd --uid "$number" --gid "$number" --no-create-home --home-dir /nonexistent --shell /usr/sbin/nologin "$name"
  fi
  [[ "$(id -u "$name")" == "$number" && "$(id -g "$name")" == "$number" ]]
}
service_identity reaper-tunnel 65532
service_identity reaper-caddy 65531

state_disk=/dev/disk/by-id/google-reaper-state
for _ in $(seq 1 120); do
  if [[ -b "$state_disk" ]]; then
    break
  fi
  sleep 1
done
test -b "$state_disk"
# Refuse unknown existing signatures; only a truly blank disk may be formatted.
fs_type=$(blkid -o value -s TYPE "$state_disk" || true)
if [[ -z "$fs_type" ]]; then
  signatures=$(wipefs --no-act --noheadings "$state_disk")
  if [[ -n "$signatures" ]]; then
    echo 'State disk has an unknown signature; refusing to format.' >&2
    exit 1
  fi
  mkfs.ext4 -q "$state_disk"
fi
if [[ -n "$fs_type" && "$fs_type" != ext4 ]]; then
  echo 'State disk is not ext4; refusing to mount or format.' >&2
  exit 1
fi
install -d -m 0750 /var/lib/reaper-state
if ! findmnt /var/lib/reaper-state > /dev/null; then
  mount "$state_disk" /var/lib/reaper-state
fi
if ! grep -q ' /var/lib/reaper-state ' /etc/fstab; then
  printf '%s /var/lib/reaper-state ext4 defaults 0 2\n' "$state_disk" >> /etc/fstab
fi
chmod 0755 /var/lib/reaper-state
install -d -m 0750 -o 65532 -g 65532 /var/lib/reaper-state/tunnel
install -d -m 0750 -o 65531 -g 65531 /var/lib/reaper-state/caddy
install -d -m 0755 /etc/caddy /usr/local/lib/reaper-tunnel

# This root-owned configuration contains identifiers only. Values are validated by Terraform.
cat > /etc/reaper-tunnel-host.conf << 'CONFIG'
project='${project}'
bucket='${bucket}'
server_key='${server_key}'
caddy_key='${caddy_key}'
admin_secret='${admin_secret}'
read_secret='${read_secret}'
domain='${domain}'
admin_actor='${admin_actor}'
CONFIG
chmod 0644 /etc/reaper-tunnel-host.conf

cat > /usr/local/lib/reaper-tunnel/cloud.sh << 'CLOUD'
#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=/dev/null
source /etc/reaper-tunnel-host.conf
access_token() {
  curl --fail --silent --show-error --retry 10 --retry-all-errors \
    -H 'Metadata-Flavor: Google' \
    http://metadata.google.internal/computeMetadata/v1/instance/service-accounts/default/token | jq -er .access_token
}
cloud_get() {
  # Header supplied on stdin keeps the short-lived token out of process arguments.
  printf 'Authorization: Bearer %s\n' "$(access_token)" | \
    curl --fail --silent --show-error --retry 10 --retry-all-errors -H @- "$1"
}
CLOUD
chmod 0755 /usr/local/lib/reaper-tunnel/cloud.sh

cat > /usr/local/lib/reaper-tunnel/render-env.sh << 'ENV'
#!/usr/bin/env bash
set -euo pipefail
# shellcheck source=/dev/null
source /usr/local/lib/reaper-tunnel/cloud.sh
secret() {
  cloud_get "https://secretmanager.googleapis.com/v1/projects/$project/secrets/$1/versions/latest:access" | jq -er .payload.data | base64 -d
}
admin_token=$(secret "$admin_secret")
read_token=$(secret "$read_secret")
[[ "$admin_token" =~ ^[a-zA-Z0-9]{48}$ && "$read_token" =~ ^[a-zA-Z0-9]{48}$ ]]
umask 077
{
  printf 'REAPER_TUNNEL_CONTROL_ADDR=127.0.0.1:8081\n'
  printf 'REAPER_TUNNEL_EDGE_ADDR=127.0.0.1:8080\n'
  printf 'REAPER_TUNNEL_DIAG_ADDR=127.0.0.1:8082\n'
  printf 'REAPER_TUNNEL_DOMAIN=%s\n' "$domain"
  printf 'REAPER_TUNNEL_DB=/var/lib/reaper-state/tunnel/tunnel.db\n'
  printf 'REAPER_TUNNEL_FORWARD_PROTO=https\n'
  printf 'REAPER_TUNNEL_ADMIN_ACTOR=%s\n' "$admin_actor"
  printf 'REAPER_TUNNEL_ADMIN_TOKEN=%s\n' "$admin_token"
  printf 'REAPER_TUNNEL_READ_TOKEN=%s\n' "$read_token"
} > /etc/reaper-tunnel.env.next
mv /etc/reaper-tunnel.env.next /etc/reaper-tunnel.env
ENV
chmod 0755 /usr/local/lib/reaper-tunnel/render-env.sh
# shellcheck source=/dev/null
source /usr/local/lib/reaper-tunnel/cloud.sh
cloud_get "https://storage.googleapis.com/$bucket/$server_key" > /usr/local/bin/reaper-tunnel.next
cloud_get "https://storage.googleapis.com/$bucket/$caddy_key" > /usr/local/bin/caddy.next
chmod 0755 /usr/local/bin/reaper-tunnel.next /usr/local/bin/caddy.next
mv /usr/local/bin/reaper-tunnel.next /usr/local/bin/reaper-tunnel
mv /usr/local/bin/caddy.next /usr/local/bin/caddy
/usr/local/lib/reaper-tunnel/render-env.sh

cat > /etc/caddy/Caddyfile << 'CADDY'
{
  email ${acme_email}
  auto_https disable_redirects
}
${domain}:8443 {
  tls {
    dns googleclouddns {
      gcp_project ${project}
    }
  }
  log {
    output stdout
    format json
  }
  reverse_proxy 127.0.0.1:8081
}
*.${domain}:443 {
  tls {
    dns googleclouddns {
      gcp_project ${project}
    }
  }
  log {
    output stdout
    format json
  }
  reverse_proxy 127.0.0.1:8080
}
CADDY
# Check the actual Caddy build and rendered syntax before starting either service.
/usr/local/bin/caddy adapt --config /etc/caddy/Caddyfile --adapter caddyfile > /dev/null

cat > /etc/systemd/system/reaper-tunnel.service << 'UNIT'
[Unit]
Description=Reaper tunnel
After=network-online.target
Wants=network-online.target
RequiresMountsFor=/var/lib/reaper-state
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
ReadWritePaths=/var/lib/reaper-state/tunnel
PrivateTmp=true
[Install]
WantedBy=multi-user.target
UNIT
cat > /etc/systemd/system/caddy.service << 'UNIT'
[Unit]
Description=Reaper TLS edge
After=network-online.target reaper-tunnel.service
Wants=network-online.target
RequiresMountsFor=/var/lib/reaper-state
[Service]
User=65531
Group=65531
Environment=XDG_DATA_HOME=/var/lib/reaper-state
Environment=XDG_CONFIG_HOME=/var/lib/reaper-state
ExecStart=/usr/local/bin/caddy run --config /etc/caddy/Caddyfile --adapter caddyfile
Restart=always
RestartSec=5
AmbientCapabilities=CAP_NET_BIND_SERVICE
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/var/lib/reaper-state/caddy
PrivateTmp=true
[Install]
WantedBy=multi-user.target
UNIT
install -d /etc/systemd/journald.conf.d
cat > /etc/systemd/journald.conf.d/reaper.conf << 'JOURNAL'
[Journal]
Storage=persistent
SystemMaxUse=100M
JOURNAL
systemctl restart systemd-journald
systemctl daemon-reload
systemctl enable reaper-tunnel.service caddy.service
systemctl restart reaper-tunnel.service caddy.service
