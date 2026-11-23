#!/usr/bin/env bash
set -euo pipefail
umask 022
export PATH=/usr/local/sbin:/usr/local/bin:/usr/sbin:/usr/bin:/sbin:/bin
src=$(cd "$(dirname "$0")/.." && pwd)
domain=${1:-}
if [[ $# -lt 1 || $# -gt 2 || ( $# -eq 2 && "$2" != --existing-proxy ) || ! "$domain" =~ ^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$ || ${#domain} -gt 253 ]]; then
  echo 'Usage: --vps inbox.example.org [--existing-proxy]' >&2; exit 2
fi
managed=1
[[ ${2:-} != --existing-proxy ]] || managed=0
die(){ echo "$*" >&2; exit 1; }
[[ $(uname -s) == Linux && -d /run/systemd/system ]] || die 'VPS mode needs Linux running systemd. For a local installation omit --vps.'
[[ $EUID == 0 ]] || die 'Run the VPS installer with sudo to register the system service.'
base=/opt/openagentinbox
state=/var/lib/openagentinbox
cfg=/etc/openagentinbox
unit=/etc/systemd/system/openagentinbox.service
caddyfile=/etc/caddy/Caddyfile
site="$cfg/site.caddy"
install -d -m 0755 "$base" "$base/releases" "$cfg"
exec 9>"$base/install.lock"
flock -n 9 || die 'Another OpenAgentInbox installation is running.'
# A repeated request for the same release and routing should preserve the running process.
marker="$base/active/deployment.id"
deployment_id=''
if [[ -f ${INBOX_INSTALL_ARCHIVE:-} ]]; then
  checksum=$(sha256sum < "$INBOX_INSTALL_ARCHIVE")
  deployment_id="${checksum%% *} $domain $managed"
  if [[ -f $marker && $(cat "$marker") == "$deployment_id" ]] &&
     systemctl is-active --quiet openagentinbox.service &&
     curl -fsS --max-time 2 http://127.0.0.1:4310/api/health >/dev/null; then
    echo "This release is already active."
    exit 0
  fi
fi
tmp=$(mktemp -d)
trap 'rm -rf -- "$tmp"' EXIT
apt_ready=0
need_packages(){
  command -v apt-get >/dev/null || die "Install $* with the system package manager and rerun."
  if (( ! apt_ready )); then apt-get update; apt_ready=1; fi
  DEBIAN_FRONTEND=noninteractive apt-get install -y --no-install-recommends "$@"
}
command -v curl >/dev/null || need_packages curl ca-certificates
if (( managed )); then
  command -v caddy >/dev/null || need_packages caddy
  [[ -f $caddyfile && ! -L $caddyfile ]] || die 'Expected the standard /etc/caddy/Caddyfile. Use --existing-proxy for a custom proxy.'
fi
if ! id openagentinbox >/dev/null 2>&1; then
  useradd --system --user-group --home-dir "$state" --no-create-home --shell "$(command -v nologin)" openagentinbox
fi
[[ $(id -gn openagentinbox) == openagentinbox && $(getent passwd openagentinbox | cut -d: -f6) == "$state" ]] || die 'Existing openagentinbox account has an unexpected group or home.'
install -d -m 0700 -o openagentinbox -g openagentinbox "$state"
# Root copies source only into newly created directories. npm always runs as the service user.
release=$(mktemp -d "$base/releases/build.XXXXXXXX")
# Distribution packages include the native daemon and prebuilt UI.
case $(uname -m) in aarch64|arm64) arch=arm64;; x86_64) arch=amd64;; *) die 'Unsupported distribution architecture';; esac
[[ -f "$src/../dist/linux-$arch/openagentinboxd" && -d "$src/../dist/ui" ]] || die 'Use the packaged installer with native binaries for VPS deployment.'
bash "$src/../scripts/build-console-release.sh" "$release"
chmod 0755 "$release"

# Preserve earlier configuration for rollback; settings.env belongs to the operator.
paths=("$unit" "$cfg/origin.env")
if (( managed )); then paths+=("$site" "$caddyfile"); fi
for i in "${!paths[@]}"; do
  if [[ -e ${paths[i]} ]]; then cp -p "${paths[i]}" "$tmp/before.$i"; fi
done
previous=$(readlink "$base/active" || true)
enabled=0; running=0
systemctl is-enabled --quiet openagentinbox.service && enabled=1
systemctl is-active --quiet openagentinbox.service && running=1
activate(){ ln -s "$1" "$base/active.next.$$"; mv -Tf "$base/active.next.$$" "$base/active"; }
publish(){
  local stage
  stage=$(mktemp "$(dirname "$2")/.openagentinbox.XXXXXXXX")
  install -m 0644 "$1" "$stage"
  if [[ -f $2 ]]; then chown --reference="$2" "$stage"; chmod --reference="$2" "$stage"; fi
  mv -fT "$stage" "$2"
}
rollback(){
  local rc=$?
  trap - EXIT
  if (( rc )); then
    echo 'Activation failed; restoring previous service configuration.' >&2
    set +e
    systemctl stop openagentinbox.service
    if [[ -n $previous ]]; then activate "$previous"; else rm -f "$base/active"; fi
    (( enabled )) || systemctl disable openagentinbox.service
    for i in "${!paths[@]}"; do
      if [[ -e $tmp/before.$i ]]; then cp -p "$tmp/before.$i" "${paths[i]}"; else rm -f "${paths[i]}"; fi
    done
    systemctl daemon-reload
    if (( running )); then echo "Service stopped. Verify database compatibility or restore the stopped pre-upgrade backup before restarting the previous release." >&2; fi
    if (( managed )); then systemctl reload caddy.service; fi
  fi
  rm -rf -- "$tmp"
  exit "$rc"
}
trap rollback EXIT
if (( managed )); then
  printf '%s {\n    reverse_proxy 127.0.0.1:4310 {\n        header_up X-Inbox-Peer-IP {http.request.remote.host}\n    }\n}\n' "$domain" > "$tmp/site"
  publish "$tmp/site" "$site"
  if ! grep -Fxq "import $site" "$caddyfile"; then
    # A permanent copy also makes the single import easy to undo by hand.
    install -d -m 0700 "$cfg/backups"
    if [[ ! -e $cfg/backups/Caddyfile.original ]]; then cp -p "$caddyfile" "$cfg/backups/Caddyfile.original"; fi
    cat "$caddyfile" > "$tmp/caddy"; printf '\nimport %s\n' "$site" >> "$tmp/caddy"
    publish "$tmp/caddy" "$caddyfile"
  fi
  caddy validate --config "$caddyfile" --adapter caddyfile
fi
printf 'INBOX_ORIGIN=https://%s\n' "$domain" > "$tmp/origin"
if (( managed ));then echo INBOX_TRUSTED_PROXY_IP=127.0.0.1 >> "$tmp/origin";fi
publish "$tmp/origin" "$cfg/origin.env"
publish "$src/deploy/openagentinbox.service" "$unit"
activate "$release"
systemctl daemon-reload
systemctl enable openagentinbox.service
systemctl restart openagentinbox.service
for ((i=0;i<60;i++)); do
  if systemctl is-active --quiet openagentinbox.service && curl -fsS --max-time 2 http://127.0.0.1:4310/.well-known/openagentinbox.json 2>/dev/null | grep -q '"version":1'; then break; fi
  sleep 1
done
(( i < 60 )) || die 'Native inbox failed its readiness check. Inspect journalctl -u openagentinbox.'
if (( managed )); then
  systemctl enable caddy.service
  systemctl reload-or-restart caddy.service
else
  echo "Forward https://$domain to http://127.0.0.1:4310 through your existing proxy."
fi
if [[ -n $deployment_id ]]; then printf '%s\n' "$deployment_id" > "$release/deployment.id"; fi
echo "Service ready on loopback. Open https://$domain after DNS and TLS are ready."
echo "For first-time setup: sudo cat $state/setup-code"
