#!/usr/bin/env bash
# Each input row is: inbox-name local-os-account. Run as the trusted host operator.
set -euo pipefail
umask 077
origin=${1:?usage: bash roster-apply.sh ORIGIN TICKET_DIRECTORY ROSTER}
tickets=${2:?expected private ticket directory}
roster=${3:?expected roster file}
case "$origin" in https://*|http://127.0.0.1:*|http://localhost:*) ;; *) echo 'Use HTTPS or loopback' >&2; exit 2;; esac
[[ -r "$roster" ]] || { echo 'Roster is unreadable' >&2; exit 1; }
mode=${4:-distinct}
[[ $mode == distinct || $mode == --shared-account ]] || exit 2
plan=$(mktemp)
trap 'rm -f "$plan"' EXIT
# Validate the entire mapping before starting any account.
while read -r name account extra || [[ -n ${name:-} ]]; do
 [[ -z "$name" || "$name" == \#* ]] && continue
 [[ $name =~ ^[a-z0-9][a-z0-9._-]{0,63}$ && $account =~ ^[a-zA-Z0-9_][a-zA-Z0-9_-]*$ && -z "$extra" ]] || { echo 'Expected inbox-name and OS-account per row' >&2; exit 2; }
 uid=$(id -u "$account")
 [[ $uid -ne 0 ]] || { echo "Invalid account for $name" >&2; exit 1; }
 printf '%s %s %s\n' "$name" "$account" "$uid" >> "$plan"
done < "$roster"
awk -v mode="$mode" 'seen[$1]++ || (mode=="distinct" && users[$3]++) {exit 1} END {if (!NR) exit 1}' "$plan" || { echo 'Use one distinct agent and OS account per row' >&2; exit 2; }
while read -r name account uid; do
 as_seat() {
   if [[ $uid == "$(id -u)" ]]; then "$@"; else sudo -n -H -u "$account" -- "$@"; fi
 }
 ticket="$tickets/$name.ticket"
 [[ -f $ticket ]] || ticket=/dev/null
 as_seat bash -c '
   set -euo pipefail
   cd "$HOME"
   origin=$1; name=$2; client="$HOME/.local/bin/openagentinbox-client"
   unset OPENAGENTINBOX_PROFILE INBOX_BIN_DIR
   if [[ ! -x $client || $("$client" version 2>/dev/null) != 0.12.11 ]];then
     installer=$(mktemp); trap '\''rm -f "$installer"'\'' EXIT
     curl -fsS "$origin/install-client.sh" -o "$installer"
     bash "$installer" "$origin" >&2
     rm -f "$installer"; trap - EXIT
   fi
   args=()
   if [[ $3 == --shared-account ]]; then args=(--seat "$name"); fi
   if "$client" ${args[@]+"${args[@]}"} verify "$origin" "$name" >/dev/null 2>&1; then echo "Verified $name"; exit 0; fi
   [[ $4 != /dev/null ]] || { echo "Missing ticket or existing verified profile for $name" >&2; exit 1; }
   "$client" ${args[@]+"${args[@]}"} join "$origin" "$name" --ticket-stdin >/dev/null
   "$client" ${args[@]+"${args[@]}"} verify "$origin" "$name"
 ' bash "$origin" "$name" "$mode" "$ticket" < "$ticket"
done < "$plan"
