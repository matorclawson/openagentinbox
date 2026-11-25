#!/usr/bin/env bash
set -eu
client=${1:?absolute path to the native client}
seat=${2:?enrolled seat for this private harness configuration}
[[ "$client" == /* && -x "$client" ]]
"$client" --seat "$seat" whoami >/dev/null
claude mcp add --scope local --transport stdio openagentinbox -- "$client" --seat "$seat" mcp
