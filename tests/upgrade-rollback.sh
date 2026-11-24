#!/usr/bin/env bash
# Prove the in-place upgrade's automatic rollback. A candidate that passes the
# loopback preflight but then fails against the service's data directory must
# leave the previous release serving, with its store and owner login intact.
#
# Run as root on a DISPOSABLE Linux systemd host that has no OpenAgentInbox
# installation. It installs the previous release and creates an owner, then runs
# three forced failures in turn and finally the real upgrade:
#   early:    the candidate exits before it opens the store;
#   migrated: the candidate migrates the real store, then is killed and exits 1;
#   late:     the candidate passes its private-port check, then fails as the service.
# After each failure the previous release must serve again from its pre-upgrade
# store copy. The last step upgrades with the unmodified candidate.
#
#   INBOX_ROLLBACK_TEST_DISPOSABLE=yes sudo -E bash tests/upgrade-rollback.sh PREVIOUS-INSTALLER CANDIDATE-INSTALLER
#
# Both arguments are packaged installers (openagentinbox-install-X.Y.Z.sh). The
# candidate must support upgrading from the previous release's version.
# Stdout is one JSON receipt; the log goes to stderr. No secret is printed.
set -euo pipefail
shopt -s nullglob
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

echo 'configure settings the private check must not inherit: a backend port set in the environment file, and the escalation mailbox' >&2
printf 'INBOX_PORT=4310\nINBOX_ESCALATION_MAILBOX=operator\n' > /etc/openagentinbox/settings.env
chmod 0600 /etc/openagentinbox/settings.env
systemctl restart openagentinbox.service
wait_version "$old" || die 'previous release did not restart with settings.env'

# The service user reads the store; root must not open it, or a root-owned -shm
# file would be left behind.
login_ok(){ [[ $(status /api/login "{\"username\":\"owner\",\"password\":\"$password\"}") == 200 ]]; }

# Install the candidate daemon behind a wrapper. $1 is the failure mode. The upgrade runs
# the candidate twice: first in a transient unit on a private port (INBOX_PORT set),
# then, if that passes, as the service on the default port. The preflight uses a private
# INBOX_DATA and always passes through.
#   early:    fail before opening the store (private phase)
#   migrated: open and migrate the real store on a second port, wait until it answers,
#             kill it, then fail (private phase). The installer never sees it healthy.
#   late:     pass the private phase, fail as the service
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
real=$real/openagentinboxd
if [[ \${INBOX_DATA:-} != /var/lib/openagentinbox || \${1:-} != serve ]]; then exec \$real "\$@"; fi
port=\${INBOX_PORT:-4310}
case $mode in
  early) echo 'forced failure early: before opening the store' >&2; exit 1;;
  late)
    if [[ \$port == 4310 ]]; then echo 'forced failure late: after readiness was proven' >&2; exit 1; fi
    # Private phase. Record where the check listens, that the public port stays closed and
    # that mail settings were removed, then behave as the real candidate.
    log=/var/lib/openagentinbox/test-private-check.log
    \$real "\$@" &
    child=\$!
    trap 'kill \$child 2>/dev/null; wait \$child 2>/dev/null; exit 0' TERM
    for ((i=0;i<240;i++)); do
      curl -fsS --max-time 1 "http://127.0.0.1:\$port/api/health" 2>/dev/null | grep -Fq '"version"' && break
      sleep 0.5
    done
    if curl -fsS --max-time 1 http://127.0.0.1:4310/api/health >/dev/null 2>&1; then echo 'public port 4310 answered during the check' > \$log
    elif [[ -n \${INBOX_ESCALATION_MAILBOX:-}\${INBOX_EMAIL_SOCKET:-}\${INBOX_EMAIL_DOMAIN:-} ]]; then echo 'mail settings reached the check' > \$log
    else echo "private check ok port=\$port" > \$log; fi
    wait \$child;;
  migrated)
    if [[ \$port == 4310 ]]; then exec \$real "\$@"; fi
    # Migrate the real store on another port so the installer's own readiness probe never
    # sees this process. Wait until the migrated store answers before killing it.
    other=\$((port + 1))
    INBOX_PORT=\$other INBOX_DEPLOY_INSTANCE=none \$real "\$@" &
    child=\$!
    ready=0
    for ((i=0;i<240;i++)); do
      if curl -fsS --max-time 1 "http://127.0.0.1:\$other/api/health" 2>/dev/null | grep -Fq '"version"'; then ready=1; break; fi
      kill -0 "\$child" 2>/dev/null || break
      sleep 0.5
    done
    kill -9 "\$child" 2>/dev/null; wait "\$child" 2>/dev/null
    if (( ready )); then echo 'forced failure migrated: migrated the store, then failed' >&2; else echo 'forced failure migrated: the candidate never became ready' >&2; fi
    exit 1;;
esac
WRAPPER
  chmod 0755 "$daemon"
}

count_matches(){ local -a m=($1); echo "${#m[@]}"; }
# Order-independent digest of the store's schema (table, index and trigger names).
schema_digest(){
  runuser -u openagentinbox -- python3 -I -c 'import sqlite3,hashlib,sys; c=sqlite3.connect("file:"+sys.argv[1]+"?mode=ro",uri=True); print(hashlib.sha256("\n".join(sorted(r[0] for r in c.execute("select type||\":\"||name from sqlite_master"))).encode()).hexdigest())' "$1"
}

forced_failure(){
  local mode=$1 marker=$2 rc=0 digest_before failed_before snaps_before failed_dir
  echo "forced failure $mode: upgrade with a candidate that fails; it must roll back" >&2
  digest_before=$(schema_digest /var/lib/openagentinbox/console.sqlite)
  failed_before=$(count_matches '/var/lib/openagentinbox/failed-upgrade-*')
  snaps_before=$(count_matches '/var/lib/openagentinbox/upgrade-snapshots/pre-*.sqlite')
  rm -f /var/lib/openagentinbox/test-private-check.log
  forced_unpack "$mode"
  bash "$work/forced/install-openagentinbox.sh" --upgrade >"$work/upgrade.out" 2>"$work/upgrade.err" || rc=$?
  cat "$work/upgrade.err" >&2
  [[ $rc != 0 ]] || die "$mode: the forced-failure upgrade reported success"
  grep -Fq 'Restored the previous running release and its data' "$work/upgrade.err" || die "$mode: installer did not report the restored release and data"
  grep -Fq "forced failure $mode:" "$work/upgrade.err" || die "$mode: the installer's log excerpt does not show the forced failure; the failure path did not run"
  [[ $mode != migrated ]] || grep -Fq 'migrated the store, then failed' "$work/upgrade.err" || die "migrated: the candidate never became ready, so the store was not migrated"
  systemctl is-active --quiet openagentinbox.service || die "$mode: service is not active after rollback"
  [[ $(version_of) == "$old" ]] || die "$mode: service does not answer on $old"
  [[ $(readlink -e /opt/openagentinbox/active) == "$before_target" ]] || die "$mode: active release is not the previous one"
  login_ok || die "$mode: owner login failed after rollback"
  [[ $(status /api/setup '{"username":"x","password":"another-long-password","code":"x"}') != 200 ]] || die "$mode: a fresh setup was accepted: data was lost"
  [[ $(status /api/login "{\"username\":\"owner\",\"password\":\"wrong-$password\"}") != 200 ]] || die "$mode: wrong password accepted"
  [[ $(schema_digest /var/lib/openagentinbox/console.sqlite) == "$digest_before" ]] || die "$mode: the restored store does not have the previous schema"
  [[ $(count_matches '/var/lib/openagentinbox/failed-upgrade-*') == $((failed_before + 1)) ]] || die "$mode: the failed store was not kept"
  [[ $(count_matches '/var/lib/openagentinbox/upgrade-snapshots/pre-*.sqlite') == $((snaps_before + 1)) ]] || die "$mode: no new pre-upgrade copy was kept"
  if [[ $mode == late ]]; then
    grep -Eq '^private check ok port=[0-9]+$' /var/lib/openagentinbox/test-private-check.log || die "late: the private check was not isolated: $(cat /var/lib/openagentinbox/test-private-check.log 2>/dev/null || echo 'no log')"
    ! grep -Fq 'port=4310' /var/lib/openagentinbox/test-private-check.log || die 'late: the private check listened on 4310'
  fi
  failed_dir=$(ls -d /var/lib/openagentinbox/failed-upgrade-* | sort | tail -n 1)
  [[ -e $failed_dir/console.sqlite ]] || die "$mode: $failed_dir holds no store"
  if [[ $mode == early ]]; then
    [[ $(schema_digest "$failed_dir/console.sqlite") == "$digest_before" ]] || die "early: the candidate changed the store before it was meant to open it"
  else
    # Proof that migration really happened: the failed store's schema differs from the
    # previous release's. If it does not, PREVIOUS already has the candidate's schema.
    [[ $(schema_digest "$failed_dir/console.sqlite") != "$digest_before" ]] || die "$mode: the failed store has the previous schema, so no migration was exercised; use an older PREVIOUS installer"
  fi
}

forced_failure early 'forced failure early:'
forced_failure migrated 'forced failure migrated:'
forced_failure late 'forced failure late:'

echo 'upgrade with the unmodified candidate: the host must still upgrade' >&2
bash "$candidate_installer" --upgrade >&2
new=$(sed -n 's/^version=//p' "$work/forced/web/deploy/upgrade-native.sh")
wait_version "$new" || die "unmodified candidate is not serving $new"
login_ok || die 'owner login failed after the real upgrade'
[[ $(count_matches '/var/lib/openagentinbox/upgrade-snapshots/pre-*.sqlite') -le 3 ]] || die 'old pre-upgrade copies were not pruned to three'
[[ $(count_matches '/var/lib/openagentinbox/failed-upgrade-*') == 3 ]] || die 'a failed store was removed'

printf '{"schema":"openagentinbox.upgrade-rollback/v2","status":"pass","variants":["early","migrated","late","real"],"previous_version":"%s","candidate_version":"%s","previous_installer_sha256":"%s","candidate_installer_sha256":"%s","arch":"%s"}\n' \
  "$old" "$new" "$(sha256sum <"$previous_installer" | cut -d' ' -f1)" "$(sha256sum <"$candidate_installer" | cut -d' ' -f1)" "$arch"
