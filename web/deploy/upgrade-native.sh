#!/usr/bin/env bash
# In-place release change for a running systemd installation.
set -euo pipefail
[[ $EUID == 0 && $# == 0 ]] || { echo 'Run sudo bash INSTALLER --upgrade on the VPS.' >&2; exit 2; }
base=/opt/openagentinbox
service=openagentinbox.service
root=$(cd "$(dirname "$0")/../.." && pwd)
version=0.12.9
health=http://127.0.0.1:4310/api/health
exec 9>"$base/install.lock"
flock -n 9 || { echo 'Another installer is running.' >&2; exit 1; }
previous=$(readlink -e "$base/active")
[[ $previous == "$base/releases/"* && -x $previous/openagentinboxd ]] || exit 1
systemctl is-active --quiet "$service"
old=$(curl -fsS --max-time 3 "$health")
case "$old" in
  *'"version":"0.12.9"'*) echo '0.12.9 is already serving.'; exit 0;;
  *'"version":"0.11.0"'*) before=0.11.0;;
  *'"version":"0.12.0"'*) before=0.12.0;;
  *'"version":"0.12.1"'*) before=0.12.1;;
  *'"version":"0.12.2"'*) before=0.12.2;;
  *'"version":"0.12.3"'*) before=0.12.3;;
  *'"version":"0.12.4"'*) before=0.12.4;;
  *'"version":"0.12.5"'*) before=0.12.5;;
  *'"version":"0.12.6"'*) before=0.12.6;;
  *'"version":"0.12.7"'*) before=0.12.7;;
  *'"version":"0.12.8"'*) before=0.12.8;;
  *'"version":"0.10.4"'*) before=0.10.4;;
  *'"version":"0.10.3"'*) before=0.10.3;;
  *'"version":"0.10.2"'*) before=0.10.2;;
  *'"version":"0.10.1"'*) before=0.10.1;;
  *'"version":"0.10.0"'*) before=0.10.0;;
  *'"version":"0.9.2"'*) before=0.9.2;;
  *'"version":"0.9.1"'*) before=0.9.1;;
  *'"version":"0.9.0"'*) before=0.9.0;;
  *'"version":"0.8.0"'*) before=0.8.0;;
  *'"version":"0.7.0"'*) before=0.7.0;;
  *'"version":"0.6.0"'*) before=0.6.0;;
  *'"version":"0.5.0"'*) before=0.5.0;;
  *) echo 'This upgrade supports a running 0.5.0, 0.6.0, 0.7.0, 0.8.0, 0.9.0, 0.9.1, 0.9.2, 0.10.0, 0.10.1, 0.10.2, 0.10.3, 0.10.4, 0.11.0, 0.12.0, 0.12.1, 0.12.2 0.12.3 0.12.4 0.12.5 0.12.6 0.12.7 or 0.12.8 installation.' >&2; exit 1;;
esac
release=$(mktemp -d "$base/releases/build.XXXXXXXX")
bash "$root/scripts/build-console-release.sh" "$release"
chmod -R a+rX "$release"
# Start under the service account with a fresh private database on loopback.
probe=$(mktemp -d)
chown openagentinbox:openagentinbox "$probe"
port=$((20000 + RANDOM % 20000))
instance=$(cat /proc/sys/kernel/random/uuid)
pid=''
cleanup(){
  if [[ -n $pid ]]; then kill "$pid" 2>/dev/null || true; wait "$pid" || true; fi
  rm -rf -- "$probe"
}
trap cleanup EXIT
runuser -u openagentinbox -- env -i PATH=/usr/bin:/bin HOME="$probe" INBOX_DATA="$probe" INBOX_PORT="$port" INBOX_DEPLOY_INSTANCE="$instance" "$release/openagentinboxd" serve >"$probe/log" 2>&1 9>&- &
pid=$!
wait_health(){
  local target=$1 needle=$2 i
  for ((i=0;i<480;i++)); do
    if curl -fsS --max-time 1 "$target" 2>/dev/null | grep -Fq "$needle"; then return 0; fi
    sleep 0.25
  done
  return 1
}
wait_health "http://127.0.0.1:$port/api/health" "$instance" &&
curl -fsS --max-time 3 "http://127.0.0.1:$port/api/health" | grep -Fq '"version":"0.12.9"' &&
curl -fsS --max-time 3 -o /dev/null "http://127.0.0.1:$port/inboxes/operator" || { echo 'Candidate preflight failed. Current server is still serving.' >&2; exit 1; }
cleanup
pid=''
trap - EXIT
activate(){ ln -s "$1" "$base/active.next.$$"; mv -Tf "$base/active.next.$$" "$base/active"; }
activate "$release"
if systemctl restart "$service" && wait_health "$health" '"version":"0.12.9"'; then
  echo "Serving $version. Previous release retained at $previous."
else
  activate "$previous"
  if systemctl restart "$service" && wait_health "$health" "\"version\":\"$before\""; then
    echo 'Upgrade failed. Restored the previous running release.' >&2
  else
    echo 'Rollback readiness failed. Inspect journalctl -u openagentinbox.' >&2
  fi
  exit 1
fi
