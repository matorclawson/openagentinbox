#!/usr/bin/env bash
# Prove the in-place upgrade's automatic rollback. A candidate that passes the
# loopback preflight but then fails against the service's data directory must
# leave the previous release serving, with its store and owner login intact.
#
# Run as root on a DISPOSABLE Linux systemd host that has no OpenAgentInbox
# installation. It installs the previous release and creates an owner, then runs
# two forced failures in turn and finally the real upgrade:
#   A. the candidate exits before it opens the store;
#   B. the candidate opens and migrates the real store, then is killed and exits
#      1, so the store is left in the candidate's schema under a failed upgrade.
# After each failure the previous release must serve again from its pre-upgrade
# store copy. The last step upgrades with the unmodified candidate.
#
#   INBOX_ROLLBACK_TEST_DISPOSABLE=yes sudo -E bash tests/upgrade-rollback.sh PREVIOUS-INSTALLER CANDIDATE-INSTALLER
#
# Both arguments are packaged installers (openagentinbox-install-X.Y.Z.sh). The
# candidate must support upgrading from the previous release's version.
# Stdout is one JSON receipt; the log goes to stderr. No secret is printed.
set -euo pipefail
die(){ echo "FAIL: $*" >&2; exit 1; }
[[ $EUID == 0 ]] || die 'run as root'
[[ ${INBOX_ROLLBACK_TEST_DISPOSABLE:-} == yes ]] || die 'set INBOX_ROLLBACK_TEST_DISPOSABLE=yes to confirm this host is disposable'
[[ $# == 2 && -f $1 && -f $2 ]] || die 'usage: upgrade-rollback.sh PREVIOUS-INSTALLER CANDIDATE-INSTALLER'
for p in /opt/openagentinbox /var/lib/openagentinbox /etc/systemd/system/openagentinbox.service; do
  [[ ! -e $p ]] || die "$p exists; refusing to touch a host that already has an installation"
done
previous_installer=$(readlink -f "$1")
candidate_installer=$(readlink -f "$2")
domain=inbox.test
origin="https://$domain"
health=http://127.0.0.1:4310/api/health
password='rollback-test-only-password'
work=$(mktemp -d)
real=/opt/oai-rollback-test
trap 'rm -rf -- "$work"' EXIT
case $(uname -m) in aarch64|arm64) arch=arm64;; x86_64) arch=amd64;; *) die 'unsupported architecture';; esac

version_of(){ curl -fsS --max-time 3 "$health" | sed -n 's/.*"version":"\([^"]*\)".*/\1/p'; }
wait_version(){
  local want=$1 i
  for ((i=0;i<120;i++)); do
    [[ $(version_of 2>/dev/null || true) == "$want" ]] && return 0
    sleep 0.5
  done
  return 1
}
status(){ curl -sS --max-time 5 -o /dev/null -w '%{http_code}' -X POST -H "Origin: $origin" -H 'content-type: application/json' --data "$2" "http://127.0.0.1:4310$1"; }
# Unpack a packaged installer's payload (the same extraction its header performs).
unpack(){
  mkdir -p "$2"
  sed -n "/^base64 -d <<'INBOX_PAYLOAD'/,/^INBOX_PAYLOAD\$/p" "$1" | sed '1d;$d' | base64 -d | tar -xz -C "$2"
}

echo '1. install the previous release' >&2
bash "$previous_installer" --vps "$domain" --existing-proxy >&2
old=$(version_of); [[ -n $old ]] || die 'previous release reports no version'
before_target=$(readlink -e /opt/openagentinbox/active)
echo "   previous version $old at $before_target" >&2

echo '2. create the owner, the data the rollback must keep' >&2
code=$(cat /var/lib/openagentinbox/setup-code)
[[ $(status /api/setup "{\"username\":\"owner\",\"password\":\"$password\",\"code\":\"$code\"}") == 200 ]] || die 'owner setup failed'
code=''
[[ $(status /api/login "{\"username\":\"owner\",\"password\":\"$password\"}") == 200 ]] || die 'login failed before the upgrade'

# The service user reads the store; root must not open it, or a root-owned -shm
# file would be left behind.
schema_objects(){
  runuser -u openagentinbox -- python3 -I -c 'import sqlite3; c=sqlite3.connect("file:/var/lib/openagentinbox/console.sqlite?mode=ro",uri=True); print(c.execute("select count(*) from sqlite_master").fetchone()[0])'
}
login_ok(){ [[ $(status /api/login "{\"username\":\"owner\",\"password\":\"$password\"}") == 200 ]]; }

# Install the candidate daemon behind a wrapper. $1 is the failure mode. The
# preflight starts the wrapper with a private INBOX_DATA and always passes it
# through; only a start against the service's data directory is made to fail.
forced_unpack(){
  local mode=$1 daemon
  rm -rf "$work/forced"
  unpack "$candidate_installer" "$work/forced"
  daemon="$work/forced/dist/linux-$arch/openagentinboxd"
  [[ -x $daemon ]] || die "candidate has no linux-$arch daemon"
  install -d -m 0755 "$real"
  [[ -x $real/openagentinboxd ]] || cp "$daemon" "$real/openagentinboxd"
  cat > "$daemon" <<WRAPPER
#!/bin/bash
export INBOX_UI="\$(dirname "\$(readlink -f "\$0")")/ui"
if [[ \${INBOX_DATA:-} != /var/lib/openagentinbox || \${1:-} != serve ]]; then exec $real/openagentinboxd "\$@"; fi
if [[ $mode == early ]]; then echo 'forced failure: before opening the store' >&2; exit 1; fi
# Migrate the real store, then fail: the store is left in the candidate's schema.
$real/openagentinboxd "\$@" &
child=\$!
for ((i=0;i<240;i++)); do
  curl -fsS --max-time 1 http://127.0.0.1:4310/api/health 2>/dev/null | grep -Fq '"version"' && break
  sleep 0.5
done
kill -9 "\$child" 2>/dev/null; wait "\$child" 2>/dev/null
echo 'forced failure: migrated the store, then failed' >&2
exit 1
WRAPPER
  chmod 0755 "$daemon"
}

forced_failure(){
  local mode=$1 marker=$2 rc=0 objects_before failed_before snaps_before
  echo "forced failure $mode: upgrade with a candidate that fails; it must roll back" >&2
  objects_before=$(schema_objects)
  failed_before=$(compgen -G '/var/lib/openagentinbox/failed-upgrade-*' | wc -l)
  snaps_before=$(compgen -G '/var/lib/openagentinbox/upgrade-snapshots/pre-*.sqlite' | wc -l)
  forced_unpack "$mode"
  bash "$work/forced/install-openagentinbox.sh" --upgrade >"$work/upgrade.out" 2>"$work/upgrade.err" || rc=$?
  cat "$work/upgrade.err" >&2
  [[ $rc != 0 ]] || die "$mode: the forced-failure upgrade reported success"
  grep -Fq 'Restored the previous running release and its data' "$work/upgrade.err" || die "$mode: installer did not report the restored release and data"
  journalctl -u openagentinbox --no-pager 2>/dev/null | grep -Fq "$marker" || die "$mode: journal does not show the forced failure; the failure path did not run"
  systemctl is-active --quiet openagentinbox.service || die "$mode: service is not active after rollback"
  [[ $(version_of) == "$old" ]] || die "$mode: service does not answer on $old"
  [[ $(readlink -e /opt/openagentinbox/active) == "$before_target" ]] || die "$mode: active release is not the previous one"
  login_ok || die "$mode: owner login failed after rollback"
  [[ $(status /api/setup '{"username":"x","password":"another-long-password","code":"x"}') != 200 ]] || die "$mode: a fresh setup was accepted: data was lost"
  [[ $(status /api/login "{\"username\":\"owner\",\"password\":\"wrong-$password\"}") != 200 ]] || die "$mode: wrong password accepted"
  [[ $(schema_objects) == "$objects_before" ]] || die "$mode: the restored store does not have the previous schema"
  [[ $(compgen -G '/var/lib/openagentinbox/failed-upgrade-*' | wc -l) == $((failed_before + 1)) ]] || die "$mode: the failed store was not kept"
  [[ $(compgen -G '/var/lib/openagentinbox/upgrade-snapshots/pre-*.sqlite' | wc -l) == $((snaps_before + 1)) ]] || die "$mode: no new pre-upgrade copy was kept"
}

forced_failure early 'forced failure: before opening the store'
forced_failure migrated 'forced failure: migrated the store, then failed'

echo 'upgrade with the unmodified candidate: the host must still upgrade' >&2
bash "$candidate_installer" --upgrade >&2
new=$(sed -n 's/^version=//p' "$work/forced/web/deploy/upgrade-native.sh")
wait_version "$new" || die "unmodified candidate is not serving $new"
login_ok || die 'owner login failed after the real upgrade'

printf '{"schema":"openagentinbox.upgrade-rollback/v2","status":"pass","variants":["early","migrated","real"],"previous_version":"%s","candidate_version":"%s","previous_installer_sha256":"%s","candidate_installer_sha256":"%s","arch":"%s"}\n' \
  "$old" "$new" "$(sha256sum <"$previous_installer" | cut -d' ' -f1)" "$(sha256sum <"$candidate_installer" | cut -d' ' -f1)" "$arch"
