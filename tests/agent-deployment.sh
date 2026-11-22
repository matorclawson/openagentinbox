#!/usr/bin/env bash
# Exercise the distributed installer on an isolated loopback port.
set -euo pipefail
installer=${1:?provide packaged installer}
state=$(mktemp -d)
pid=''
trap 'if [[ -n $pid ]]; then kill "$pid" 2>/dev/null || true; fi; rm -rf -- "$state"' EXIT
export INBOX_INSTALL_DIR="$state/install" INBOX_DATA="$state/data" INBOX_BIN_DIR="$state/bin"
port=$(python3 -c 'import socket; s=socket.socket(); s.bind(("127.0.0.1",0)); print(s.getsockname()[1])')
call(){ bash "$installer" agent "$1" --target local --port "$port"; }
call plan >"$state/plan"
[[ ! -e "$INBOX_INSTALL_DIR" ]]
rc=0; call status >"$state/before" || rc=$?; [[ $rc == 2 ]]
call apply >"$state/apply"
pid=$(python3 -c 'import json,sys; print(json.load(open(sys.argv[1]))["pid"])' "$state/apply")
call status >"$state/status"
call apply >"$state/repeat"
rc=0; INBOX_DATA="$state/another" call apply >"$state/collision" || rc=$?; [[ $rc == 1 ]]
python3 - "$state" <<'PY'
import json, pathlib, sys, urllib.request
root=pathlib.Path(sys.argv[1])
def read(name):return json.loads((root/name).read_bytes())
a=read("apply")
assert read("before")["status"]=="not_ready"
assert a["status"]=="ready" and a["owner_setup_required"] and a["changed"]
assert a["next"]=="owner_setup" and pathlib.Path(a["setup_code_file"]).is_file()
assert read("repeat")["changed"] is False and read("status")["status"]=="ready"
assert read("collision")["status"]=="error"
assert json.load(urllib.request.urlopen(a["discovery_url"]))["mcp"]=="/mcp"
assert len(list((root/"install/releases").iterdir()))==1
assert (root/"data/setup-code").read_text() not in (root/"apply").read_text()
print("PASS: plan, absent status, detached apply, discovery, repeat, data-directory conflict and private owner handoff")
PY
