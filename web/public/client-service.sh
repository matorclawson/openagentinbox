#!/usr/bin/env bash
# Register one watcher per OS user and private profile.
set -euo pipefail
umask 077
action=${1:-start}
bin=${INBOX_BIN_DIR:-"$HOME/.local/bin"}/openagentinbox-client
profile=${OPENAGENTINBOX_PROFILE:-"$HOME/.config/openagentinbox/agent.json"}
case "$profile" in /*) ;; *) echo 'Use an absolute profile path' >&2; exit 2;; esac
[[ $action == start || $action == stop || $action == status ]] || exit 2
case "$(uname -s)" in
 Darwin) hash=$(printf %s "$profile" | shasum -a 256);;
 Linux) hash=$(printf %s "$profile" | sha256sum);;
 *) exit 2;;
esac
label="org.openagentinbox.watch.${hash:0:16}"
# Place paths in a private wrapper using Bash's shell quoting.
wrapper="$HOME/.local/share/openagentinbox-client/$label"
if [[ $action == start ]]; then
 [[ -f $profile && -x $bin ]] || { echo 'Install the client and enroll first.' >&2; exit 1; }
 mkdir -p "$(dirname "$wrapper")"
 tmp=$(mktemp "$wrapper.XXXXXXXX")
 printf '#!/bin/bash\nexport OPENAGENTINBOX_PROFILE=%q\nexec %q watch\n' "$profile" "$bin" > "$tmp"
 chmod 700 "$tmp"; mv -f "$tmp" "$wrapper"
fi
if [[ $(uname -s) == Darwin ]]; then
 plist="$HOME/Library/LaunchAgents/$label.plist"
 domain="gui/$(id -u)"
 case "$action" in
 start)
 mkdir -p "$(dirname "$plist")"
 plutil -create xml1 "$plist.tmp"
 plutil -insert Label -string "$label" "$plist.tmp"
 plutil -insert ProgramArguments -xml '<array/>' "$plist.tmp"
 plutil -insert ProgramArguments.0 -string "$wrapper" "$plist.tmp"
 plutil -insert RunAtLoad -bool true "$plist.tmp"
 plutil -insert KeepAlive -bool true "$plist.tmp"
 plutil -insert ThrottleInterval -integer 30 "$plist.tmp"
 mv -f "$plist.tmp" "$plist"
 launchctl print "$domain/$label" >/dev/null 2>&1 || launchctl bootstrap "$domain" "$plist";;
 status) launchctl print "$domain/$label";;
 stop) launchctl bootout "$domain/$label"; rm -f "$plist";;
 esac
else
 unit="$HOME/.config/systemd/user/$label.service"
 case "$action" in
 start)
 mkdir -p "$(dirname "$unit")"
 cat > "$unit.tmp" <<UNIT
[Unit]
Description=OpenAgentInbox remote watcher
[Service]
ExecStart="%h/.local/share/openagentinbox-client/$label"
Restart=on-failure
RestartSec=30
[Install]
WantedBy=default.target
UNIT
 mv -f "$unit.tmp" "$unit"
 systemctl --user daemon-reload; systemctl --user enable --now "$label.service";;
 status) systemctl --user status "$label.service";;
 stop) systemctl --user disable --now "$label.service";;
 esac
fi
