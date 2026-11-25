#!/usr/bin/env bash
set -eu
client=${1:?absolute path to the native client}
seat=${2:?enrolled seat for this private harness configuration}
[[ "$client" == /* && -x "$client" ]]
"$client" --seat "$seat" whoami >/dev/null
grok mcp add openagentinbox -- "$client" --seat "$seat" mcp
