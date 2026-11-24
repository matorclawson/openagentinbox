#!/usr/bin/env bash
# In-place release change for a running systemd installation.
set -euo pipefail
[[ $EUID == 0 && $# == 0 ]] || { echo 'Run sudo bash INSTALLER --upgrade on the VPS.' >&2; exit 2; }
base=/opt/openagentinbox
service=openagentinbox.service
root=$(cd "$(dirname "$0")/../.." && pwd)
version=0.12.11
health=http://127.0.0.1:4310/api/health
exec 9>"$base/install.lock"
flock -n 9 || { echo 'Another installer is running.' >&2; exit 1; }
previous=$(readlink -e "$base/active")
[[ $previous == "$base/releases/"* && -x $previous/openagentinboxd ]] || exit 1
systemctl is-active --quiet "$service"
old=$(curl -fsS --max-time 3 "$health")
case "$old" in
  *'"version":"0.12.11"'*) echo '0.12.11 is already serving.'; exit 0;;
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
  *'"version":"0.12.9"'*) before=0.12.9;;
  *'"version":"0.12.10"'*) before=0.12.10;;
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
  *) echo 'This upgrade supports a running 0.5.0, 0.6.0, 0.7.0, 0.8.0, 0.9.0, 0.9.1, 0.9.2, 0.10.0, 0.10.1, 0.10.2, 0.10.3, 0.10.4, 0.11.0, 0.12.0, 0.12.1, 0.12.2 0.12.3 0.12.4 0.12.5 0.12.6 0.12.7 0.12.8 or 0.12.9 installation.' >&2; exit 1;;
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
# wait_health URL NEEDLE [UNIT]: poll until the page contains NEEDLE. With a UNIT, give up
# early when that unit is no longer active instead of waiting out the full two minutes.
wait_health(){
  local target=$1 needle=$2 unit=${3:-} i
  for ((i=0;i<480;i++)); do
    if curl -fsS --max-time 1 "$target" 2>/dev/null | grep -Fq "$needle"; then return 0; fi
    if [[ -n $unit && $i -ge 8 ]] && ! systemctl is-active --quiet "$unit"; then return 1; fi
    sleep 0.25
  done
  return 1
}
wait_health "http://127.0.0.1:$port/api/health" "$instance" &&
curl -fsS --max-time 3 "http://127.0.0.1:$port/api/health" | grep -Fq '"version":"0.12.11"' &&
curl -fsS --max-time 3 -o /dev/null "http://127.0.0.1:$port/inboxes/operator" || { echo 'Candidate preflight failed. Current server is still serving.' >&2; exit 1; }
cleanup
pid=''
trap - EXIT
activate(){ ln -s "$1" "$base/active.next.$$"; mv -Tf "$base/active.next.$$" "$base/active"; }
data=/var/lib/openagentinbox
db=$data/console.sqlite
snaps=$data/upgrade-snapshots
check=openagentinbox-upgrade-check
# BEGIN confirm_stopped
# confirm_stopped UNIT: stop it and prove it is gone. Every query must succeed and show the
# unit inactive or failed, no main process, and no process in its control group, read from
# the group's member list. A query that fails counts as not confirmed.
cgroup_members(){
  local f=/sys/fs/cgroup$1/cgroup.procs
  [[ -n $1 && -e $f ]] || return 0
  cat "$f" 2>/dev/null || [[ ! -e $f ]]
}
confirm_stopped(){
  local unit=$1 i state pid cg members
  systemctl stop "$unit" 2>/dev/null || true
  for ((i=0;i<120;i++)); do
    if state=$(systemctl show -p ActiveState --value "$unit" 2>/dev/null) &&
       pid=$(systemctl show -p MainPID --value "$unit" 2>/dev/null) &&
       cg=$(systemctl show -p ControlGroup --value "$unit" 2>/dev/null) &&
       [[ $state == inactive || $state == failed ]] && [[ $pid == 0 ]] &&
       members=$(cgroup_members "$cg") && [[ -z $members ]]; then
      return 0
    fi
    sleep 0.5
  done
  return 1
}
# END confirm_stopped
show_logs(){
  journalctl -u "$check" -u "$service" --no-pager -n 30 -o cat 2>/dev/null | sed 's/^/  | /' >&2 || true
}
[[ -f $db ]] || { echo "No store at $db. Current server is still serving." >&2; exit 1; }
used=$(( $(stat -c %s "$db") + $(stat -c %s "$db-wal" 2>/dev/null || echo 0) ))
avail=$(df --output=avail -B1 "$data" | tail -n 1)
(( avail > 2 * used )) || { echo 'Not enough free disk space for a pre-upgrade copy. Current server is still serving.' >&2; exit 1; }
install -d -m 0700 -o openagentinbox -g openagentinbox "$snaps"
stamp=$(date -u +%Y%m%dT%H%M%SZ)
snap="$snaps/pre-$before-to-$version-$stamp.sqlite"
failed="$data/failed-upgrade-$stamp"
# From here the store may change, so the previous service stops first and must be
# confirmed gone. No message accepted before the stop is missing from the copy, and none
# can arrive between the copy and the switch.
if ! confirm_stopped "$service"; then
  systemctl start "$service" || true
  echo 'Could not confirm the previous service stopped. Nothing was changed.' >&2
  exit 1
fi
if ! manifest=$(runuser -u openagentinbox -- "$release/openagentinboxd" snapshot "$db" "$snap"); then
  systemctl start "$service" || true
  echo 'Could not take the pre-upgrade copy. The store is unchanged; restarted the previous release.' >&2
  exit 1
fi
snap_sha=$(printf '%s' "$manifest" | sed -n 's/.*"sha256":"\([0-9a-f]\{64\}\)".*/\1/p')
if [[ -z $snap_sha ]]; then
  systemctl start "$service" || true
  echo 'The pre-upgrade copy reported no checksum. The store is unchanged; restarted the previous release.' >&2
  exit 1
fi
# BEGIN restore_store
# archive_failed_store DB DIR: keep the active store and its sidecars in DIR under names no
# earlier archive uses, so a retried restore never overwrites the first archive.
archive_failed_store(){
  local db=$1 dir=$2 n=0 suffix='' f
  install -d -m 0700 -o openagentinbox -g openagentinbox "$dir"
  while [[ -e $dir/console.sqlite$suffix ]]; do n=$((n+1)); suffix=.$n; done
  ln "$db" "$dir/console.sqlite$suffix"
  for f in "$db-wal" "$db-shm"; do [[ ! -e $f ]] || mv -n "$f" "$dir/$(basename "$f")$suffix"; done
}
# restore_store DB FAILED-DIR SNAPSHOT SNAPSHOT-SHA256 STAGING: console.sqlite is never absent.
# The active store is hard-linked into the archive, then the copy replaces it by atomic
# rename. If the active store already is the copy, an earlier run finished the replacement,
# and it is left alone: it is never archived as a failed store. Run it again after any
# interruption.
restore_store(){
  local db=$1 dir=$2 snap=$3 sha=$4 staging=$5
  if [[ $(sha256sum < "$db" | cut -d' ' -f1) == "$sha" ]]; then return 0; fi
  archive_failed_store "$db" "$dir"
  install -m 0600 -o openagentinbox -g openagentinbox "$snap" "$staging"
  mv -T "$staging" "$db"
}
# END restore_store
# Undo a failed upgrade: confirm nothing else holds the store, keep the failed store for
# reconciliation, restore the copy by atomic rename, and start the previous release.
# If this process dies partway, the copy is intact; stop both units and copy it over
# console.sqlite after saving the active file under a new name.
rollback(){
  show_logs
  if ! confirm_stopped "$check" || ! confirm_stopped "$service"; then
    echo "Could not confirm the upgrade processes stopped, so the store was not touched. The pre-upgrade copy is $snap. Stop openagentinbox.service and $check.service, then restore it by hand." >&2
    exit 1
  fi
  # The copy must still match the checksum taken when it was written.
  if [[ $(sha256sum < "$snap" | cut -d' ' -f1) != "$snap_sha" ]]; then
    echo "The pre-upgrade copy $snap no longer matches its checksum, so the store was not touched. Do not restore it." >&2
    exit 1
  fi
  trap '' INT TERM HUP
  restore_store "$db" "$failed" "$snap" "$snap_sha" "$data/.restore.$stamp"
  trap - INT TERM HUP
  if [[ $(sha256sum < "$db" | cut -d' ' -f1) != "$snap_sha" ]]; then
    echo "The restored store does not match the pre-upgrade copy $snap. The previous release was not started. The failed store is in $failed." >&2
    exit 1
  fi
  activate "$previous"
  if systemctl start "$service" && wait_health "$health" "\"version\":\"$before\"" "$service"; then
    echo "Upgrade failed. Restored the previous running release and its data from $snap. The failed store is kept in $failed; it holds anything the candidate wrote after the copy." >&2
  else
    echo "Rollback readiness failed. The pre-upgrade copy is $snap and the failed store is in $failed. Inspect journalctl -u openagentinbox." >&2
  fi
  exit 1
}
# Start the candidate against the real store on a private loopback port, in a transient
# unit with the service's account, environment files and sandbox. Nothing reaches it
# through the public listener or the reverse proxy until it has migrated the store and
# answered. A candidate that fails here is rolled back before it could accept public writes.
cport=4310
while [[ $cport == 4310 ]] || (exec 3<>"/dev/tcp/127.0.0.1/$cport") 2>/dev/null; do cport=$((20000 + RANDOM % 20000)); done
cinstance=$(cat /proc/sys/kernel/random/uuid)
# The service's environment files override Environment= properties, so the port and the
# other settings that matter are applied by env(1) on the command line, where they win.
# Mail transport, the escalation mailbox and any non-loopback bind are removed: an
# unproven candidate must not receive mail, send mail or listen beyond loopback.
envbin=$(command -v env)
systemd-run --quiet --unit="$check" --collect --property=User=openagentinbox --property=Group=openagentinbox \
  --property=WorkingDirectory="$release" --property=UMask=0077 --property=NoNewPrivileges=yes --property=PrivateTmp=yes \
  --property=ProtectHome=yes --property=ProtectSystem=strict --property=ReadWritePaths="$data" \
  --property=Environment=INBOX_DATA="$data" --property=EnvironmentFile=/etc/openagentinbox/origin.env \
  --property=EnvironmentFile=-/etc/openagentinbox/settings.env \
  "$envbin" -u INBOX_EMAIL_DOMAIN -u INBOX_EMAIL_SOCKET -u INBOX_EMAIL_RELAY -u INBOX_EMAIL_SENDERS -u INBOX_ESCALATION_MAILBOX \
  INBOX_DATA="$data" INBOX_LISTEN=127.0.0.1 INBOX_PORT="$cport" INBOX_DEPLOY_INSTANCE="$cinstance" \
  "$release/openagentinboxd" serve || rollback
wait_health "http://127.0.0.1:$cport/api/health" "$cinstance" "$check" &&
curl -fsS --max-time 3 "http://127.0.0.1:$cport/api/health" | grep -Fq '"version":"0.12.11"' || rollback
confirm_stopped "$check" || rollback
activate "$release"
# From the moment the public listener is reachable, the candidate may have accepted writes
# that a restored copy would lose. wait_public records whether it ever was reachable. If
# the start fails before that, rollback restores the copy. If it was reachable, the
# release moves forward only: nothing is restored, and the copy is kept for reconciliation.
public_seen=0
wait_public(){
  local i
  for ((i=0;i<480;i++)); do
    if (exec 3<>"/dev/tcp/127.0.0.1/4310") 2>/dev/null; then public_seen=1; fi
    if curl -fsS --max-time 1 "$health" 2>/dev/null | grep -Fq '"version":"0.12.11"'; then return 0; fi
    if [[ $i -ge 8 ]] && ! systemctl is-active --quiet "$service"; then return 1; fi
    sleep 0.25
  done
  return 1
}
if systemctl start "$service" && wait_public; then
  echo "Serving $version. Previous release retained at $previous. Pre-upgrade copy: $snap"
  # Keep the newest three copies. Failed stores are never removed here.
  mapfile -t stale < <(ls -1t "$snaps"/pre-*.sqlite 2>/dev/null | tail -n +4)
  (( ${#stale[@]} == 0 )) || rm -f -- "${stale[@]}"
elif (( public_seen )) || (exec 3<>"/dev/tcp/127.0.0.1/4310") 2>/dev/null; then
  show_logs
  echo "Upgrade failed after the public listener was reachable, so writes may have been accepted. Nothing was restored. The active release is $release and the store at $db is in use. The pre-upgrade copy is $snap. Fix forward, or stop openagentinbox.service, reconcile the accepted writes and restore the copy by hand." >&2
  exit 1
else
  rollback
fi
