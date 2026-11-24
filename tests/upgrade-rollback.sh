#!/usr/bin/env bash
# Prove the in-place upgrade's automatic rollback. A candidate that passes the
# loopback preflight but fails to start against the service's data directory must
# leave the previous release serving, with its data and owner login intact.
#
# Run as root on a DISPOSABLE Linux systemd host that has no OpenAgentInbox
# installation. It installs the previous release, creates an owner, forces the
# candidate to fail at the real restart, then checks recovery. A final step
# upgrades with the unmodified candidate to show the host is still upgradable.
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

echo '3. build a candidate that passes the preflight and fails at the real restart' >&2
unpack "$candidate_installer" "$work/forced"
new=$(sed -n 's/^version=//p' "$work/forced/web/deploy/upgrade-native.sh")
[[ -n $new && $new != "$old" ]] || die "cannot determine a candidate version different from $old"
daemon="$work/forced/dist/linux-$arch/openagentinboxd"
[[ -x $daemon ]] || die "candidate has no linux-$arch daemon"
install -d -m 0755 "$real"
install -m 0755 "$daemon" "$real/openagentinboxd"
cat > "$daemon" <<EOF
#!/bin/bash
# Test wrapper. The preflight runs it with a private INBOX_DATA and passes; the
# service runs it against the real data directory and fails before opening it.
if [[ \${INBOX_DATA:-} == /var/lib/openagentinbox ]]; then echo 'forced failure' >&2; exit 1; fi
export INBOX_UI="\$(dirname "\$(readlink -f "\$0")")/ui"
exec $real/openagentinboxd "\$@"
EOF
chmod 0755 "$daemon"

echo '4. upgrade with the failing candidate: it must fail and roll back' >&2
rc=0
bash "$work/forced/install-openagentinbox.sh" --upgrade >"$work/upgrade.out" 2>"$work/upgrade.err" || rc=$?
cat "$work/upgrade.err" >&2
[[ $rc != 0 ]] || die 'the forced-failure upgrade reported success'
grep -Fq 'Restored the previous running release' "$work/upgrade.err" || die 'installer did not report the restored release'
journalctl -u openagentinbox --no-pager 2>/dev/null | grep -Fq 'forced failure' || die 'journal does not show the forced start failure; the failure path did not run'

echo '5. the previous release serves again with its data' >&2
systemctl is-active --quiet openagentinbox.service || die 'service is not active after rollback'
[[ $(version_of) == "$old" ]] || die "service does not answer on $old"
[[ $(readlink -e /opt/openagentinbox/active) == "$before_target" ]] || die 'active release is not the previous one'
[[ $(status /api/login "{\"username\":\"owner\",\"password\":\"$password\"}") == 200 ]] || die 'owner login failed after rollback'
[[ $(status /api/setup '{"username":"x","password":"another-long-password","code":"x"}') != 200 ]] || die 'a fresh setup was accepted: data was lost'
# The old version still enforces authentication.
[[ $(status /api/login "{\"username\":\"owner\",\"password\":\"wrong-$password\"}") != 200 ]] || die 'wrong password accepted'

echo '6. upgrade with the unmodified candidate: the host must still upgrade' >&2
bash "$candidate_installer" --upgrade >&2
wait_version "$new" || die "unmodified candidate is not serving $new"
[[ $(status /api/login "{\"username\":\"owner\",\"password\":\"$password\"}") == 200 ]] || die 'owner login failed after the real upgrade'

printf '{"schema":"openagentinbox.upgrade-rollback/v1","status":"pass","previous_version":"%s","candidate_version":"%s","previous_installer_sha256":"%s","candidate_installer_sha256":"%s","arch":"%s"}\n' \
  "$old" "$new" "$(sha256sum <"$previous_installer" | cut -d' ' -f1)" "$(sha256sum <"$candidate_installer" | cut -d' ' -f1)" "$arch"
