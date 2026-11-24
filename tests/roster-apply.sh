#!/usr/bin/env bash
set -euo pipefail
umask 077

root=$(cd "$(dirname "$0")/.." && pwd)
fixture=$(mktemp -d)
trap 'rm -rf -- "$fixture"' EXIT
export HOME="$fixture/home"
export FIXTURE_LOG="$fixture/events"
export FIXTURE_ORIGIN=http://127.0.0.1:4100
export FIXTURE_VERSION
FIXTURE_VERSION=$(cat "$root/VERSION")
export PATH="$fixture/bin:$PATH"
mkdir -p "$HOME/.local/bin" "$fixture/bin" "$fixture/tickets"
touch "$FIXTURE_LOG"

cat > "$fixture/bin/id" <<'STUB'
#!/usr/bin/env bash
set -eu
[[ $1 == -u ]] || exit 90
case ${2:-} in
  ''|fixture-user|fixture-alias) echo 811 ;;
  *) exit 91 ;;
esac
STUB
for command in curl sudo; do
  cat > "$fixture/bin/$command" <<'STUB'
#!/usr/bin/env bash
printf 'Unexpected external operation: %s\n' "${0##*/}" >> "$FIXTURE_LOG"
exit 92
STUB
done
cat > "$HOME/.local/bin/openagentinbox-client" <<'STUB'
#!/usr/bin/env bash
set -euo pipefail
if [[ $1 == version ]]; then
  printf '%s\n' "$FIXTURE_VERSION"
  exit
fi
seat=
if [[ $1 == --seat ]]; then
  seat=$2
  shift 2
fi
action=$1
origin=$2
name=$3
shift 3
printf '%s %s %s\n' "$action" "$name" "${seat:-default}" >> "$FIXTURE_LOG"
[[ $origin == "$FIXTURE_ORIGIN" ]] || exit 93
[[ -z $seat || $seat == "$name" ]] || exit 94
profile="$HOME/${seat:-default}.verified"
case $action in
  verify) [[ $# == 0 && -f $profile && $(cat "$profile") == "$name" ]] ;;
  join)
    [[ $# == 1 && $1 == --ticket-stdin ]] || exit 95
    [[ $(cat) == "fixture-ticket-$name" ]] || exit 96
    printf '%s\n' "$name" > "$profile"
    ;;
  *) exit 97 ;;
esac
STUB
chmod +x "$fixture/bin/"* "$HOME/.local/bin/openagentinbox-client"

roster="$fixture/roster"
apply() {
  bash "$root/web/public/roster-apply.sh" "$FIXTURE_ORIGIN" "$fixture/tickets" "$roster" "$@"
}
expect_failure() {
  local diagnostic=$1
  shift
  if apply "$@" > "$fixture/output" 2>&1; then
    echo "Roster unexpectedly succeeded (wanted $diagnostic)" >&2
    exit 1
  fi
  if ! grep -Fq "$diagnostic" "$fixture/output"; then
    cat "$fixture/output" >&2
    exit 1
  fi
}
expect_events() {
  printf '%s\n' "$@" > "$fixture/expected"
  diff -u "$fixture/expected" "$FIXTURE_LOG"
  : > "$FIXTURE_LOG"
}

printf 'first fixture-user\nINVALID fixture-user extra\n' > "$roster"
expect_failure 'Expected inbox-name and OS-account per row'
printf 'first fixture-user\nsecond fixture-alias\n' > "$roster"
expect_failure 'Use one distinct agent and OS account per row'
printf 'first fixture-user\nfirst fixture-alias\n' > "$roster"
expect_failure 'Use one distinct agent and OS account per row' --shared-account
if [[ -s $FIXTURE_LOG ]]; then
  echo 'Roster invoked a client before validating every row' >&2
  cat "$FIXTURE_LOG" >&2
  exit 1
fi

printf 'first fixture-user\n' > "$roster"
expect_failure 'Missing ticket or existing verified profile for first'
expect_events 'verify first default'
printf 'fixture-ticket-first\n' > "$fixture/tickets/first.ticket"
apply >/dev/null
expect_events 'verify first default' 'join first default' 'verify first default'

printf '# shared account fixture\nfirst fixture-user\nsecond fixture-user\n' > "$roster"
printf 'first\n' > "$HOME/first.verified"
printf 'fixture-ticket-second\n' > "$fixture/tickets/second.ticket"
apply --shared-account >/dev/null
expect_events 'verify first first' 'verify second second' 'join second second' 'verify second second'
rm "$fixture/tickets/second.ticket"
apply --shared-account >/dev/null
expect_events 'verify first first' 'verify second second'

echo 'Roster fixture tests passed.'
