#!/usr/bin/env bash
# Opt-in network probe; never invoked by the loopback proof suite.
set -euo pipefail
if [[ $# != 1 || "$1" != --probe-public-endpoints ]]; then
  echo 'Usage: bash egress.sh --probe-public-endpoints' >&2
  exit 2
fi
command -v curl > /dev/null
failed=0
probe() {
  local name=$1 url=$2 status
  # Ignore curlrc/proxies, force IPv6 and TLS verification, discard all response bodies.
  # An HTTP rejection proves transport only; no credentials or project identifiers are sent.
  if ! status=$(curl -q --noproxy '*' -6 --silent --location \
    --proto '=https' --proto-redir '=https' --max-redirs 3 \
    --connect-timeout 5 --max-time 15 --output /dev/null \
    --write-out '%{http_code}' "$url"); then
    printf 'FAIL %s transport\n' "$name"
    failed=1
    return
  fi
  if [[ ! "$status" =~ ^[1-5][0-9][0-9]$ ]]; then
    printf 'FAIL %s no-http-response\n' "$name"
    failed=1
    return
  fi
  printf 'REACHABLE %s http=%s (not an authorization or bootstrap proof)\n' "$name" "$status"
}
probe debian https://deb.debian.org/debian/dists/bookworm/InRelease
probe debian-security https://security.debian.org/debian-security/dists/bookworm-security/InRelease
probe google-packages https://packages.cloud.google.com/apt/doc/apt-key.gpg
probe artifacts https://storage.googleapis.com/
probe secrets https://secretmanager.googleapis.com/
probe dns-api https://dns.googleapis.com/
probe acme-primary https://acme-v02.api.letsencrypt.org/directory
probe acme-fallback https://acme.zerossl.com/v2/DV90
exit "$failed"
