"""Run with INBOX_DAEMON and INBOX_CLIENT pointing to the test binaries. Requires pip install mcp."""
import asyncio
import json
import os
import socket
import subprocess
import tempfile
import time
from pathlib import Path
from urllib.request import Request, urlopen
from uuid import uuid4
import sys

ROOT = Path(__file__).resolve().parents[1]
sys.path[:0] = [str(ROOT / "integrations/python"), str(ROOT / "examples/harnesses")]
from agi_real import report
from openagentinbox import Inbox, InboxError
from python_tools import list_mail, mailbox_identity, read_mail
from mcp import ClientSession, StdioServerParameters
from mcp.client.stdio import stdio_client
from mcp.client.streamable_http import streamable_http_client
import httpx


async def check(transport, message_id):
    async with transport as streams:
        async with ClientSession(*streams[:2]) as session:
            await session.initialize()
            tools = await session.list_tools()
            assert {"whoami", "read", "send", "request", "threads"} <= {t.name for t in tools.tools}
            result = await session.call_tool("whoami", {}, meta={"progressToken":"demo"})
            assert json.loads(result.content[0].text)["name"] == "reviewer"
            result = await session.call_tool("read", {"id": message_id})
            assert json.loads(result.content[0].text)["body"] == "fixture"
            key = str(uuid4())
            args = {"to":"builder", "subject":"Re: Test", "body":"Received", "key":key,"replyTo":message_id}
            sent = await session.call_tool("send", args)
            replay = await session.call_tool("send", args)
            assert not sent.isError and json.loads(sent.content[0].text)["id"] == json.loads(replay.content[0].text)["id"]
            assert (await session.call_tool("read", {"id": str(uuid4())})).isError


async def denied(transport, message_id):
    """An authorized seat that is not a party to an existing message must not read it."""
    async with transport as streams:
        async with ClientSession(*streams[:2]) as session:
            await session.initialize()
            assert json.loads((await session.call_tool("whoami", {})).content[0].text)["name"] == "unrelated"
            result = await session.call_tool("read", {"id": message_id})
            assert result.isError, "unrelated mailbox read an existing message over MCP"
            assert "fixture" not in "".join(getattr(c, "text", "") for c in result.content)
            listed = await session.call_tool("threads", {})
            assert message_id not in "".join(getattr(c, "text", "") for c in listed.content)


def exercise(origin, root, code):
    def post(path, data, cookie=""):
        req = Request(origin+"/api/"+path, json.dumps(data).encode(),
                      {"Content-Type":"application/json","Origin":origin,"Cookie":cookie})
        return urlopen(req, timeout=5)
    with post("setup", {"username":"owner","password":"temporary-test-password","code":code}) as res:
        cookie = res.headers["Set-Cookie"].split(";")[0]
    profiles = {}
    for name in ("builder","reviewer","unrelated"):
        with post("desk/agents", {"name":name}, cookie) as res:
            token = json.load(res)["credential"]
        f = root / ".config/openagentinbox/seats" / (name+".json")
        f.parent.mkdir(exist_ok=True,parents=True)
        f.touch(mode=0o600)
        f.write_text(json.dumps({"origin":origin,"token":token,"name":name}))
        profiles[name] = f
    os.environ["OPENAGENTINBOX_PROFILE"] = str(profiles["builder"])
    assert mailbox_identity()["name"] == "builder"
    sender = Inbox.from_env()
    key = str(uuid4())
    message = sender.send("reviewer","Test","fixture",key)["id"]
    assert sender.send("reviewer","Test","fixture",key)["id"] == message
    os.environ["OPENAGENTINBOX_PROFILE"] = str(profiles["reviewer"])
    assert [r["id"] for r in list_mail()["rows"]] == [message]
    assert read_mail(message)["body"] == "fixture"
    assert sender.receipt(message)
    try:
        Inbox(profiles["unrelated"]).read(message)
    except InboxError:
        pass
    else:
        raise AssertionError("unrelated mailbox could read message")
    assert report(key,"builder","completed")["id"] == report(key,"builder","completed")["id"]
    import shutil
    if shutil.which("codex"):
        config = root / "private-codex"
        config.mkdir()
        env = {**os.environ, "HOME":str(root),"CODEX_HOME":str(config)}
        subprocess.run(["bash",str(ROOT/"examples/harnesses/codex/register.sh"),os.environ["INBOX_CLIENT"],"reviewer"],env=env,check=True)
        got = subprocess.check_output(["codex","mcp","get","openagentinbox","--json"],env=env)
        assert json.loads(got)["transport"]["args"] == ["--seat","reviewer","mcp"]
        print("PASS Codex CLI add/get in disposable configuration")
    params = StdioServerParameters(command=os.environ["INBOX_CLIENT"],args=["--seat","reviewer","mcp"],
                                  env={"HOME":str(root),"OPENAGENTINBOX_PROFILE":str(profiles["unrelated"])})
    asyncio.run(check(stdio_client(params), message))
    bearer = json.loads(profiles["reviewer"].read_text())["token"]
    async def remote():
        async with httpx.AsyncClient(headers={"Authorization":"Bearer "+bearer}, trust_env=False) as http:
            await check(streamable_http_client(origin+"/mcp",http_client=http),message)
    asyncio.run(remote())
    other = StdioServerParameters(command=os.environ["INBOX_CLIENT"],args=["--seat","unrelated","mcp"],
                                  env={"HOME":str(root),"OPENAGENTINBOX_PROFILE":str(profiles["reviewer"])})
    asyncio.run(denied(stdio_client(other), message))
    other_bearer = json.loads(profiles["unrelated"].read_text())["token"]
    async def remote_denied():
        async with httpx.AsyncClient(headers={"Authorization":"Bearer "+other_bearer}, trust_env=False) as http:
            await denied(streamable_http_client(origin+"/mcp",http_client=http),message)
    asyncio.run(remote_denied())
    print("PASS an unrelated seat is denied an existing message over MCP stdio and HTTP")
    print("PASS Python send/replay, list, read, receipt, authorization and REAL completion reporting")
    print("PASS MCP SDK stdio and HTTP initialize, list, identity, read and tool errors; explicit seat overrides ambient profile")


if __name__ == "__main__":
    with tempfile.TemporaryDirectory() as temp:
        root = Path(temp)
        with socket.socket() as s:
            s.bind(("127.0.0.1",0))
            port = s.getsockname()[1]
        origin = f"http://127.0.0.1:{port}"
        (root/"index.html").write_text("fixture")
        proc = subprocess.Popen([os.environ["INBOX_DAEMON"],"serve"],env={**os.environ,
            "INBOX_DATA":temp,"INBOX_UI":temp,"INBOX_PORT":str(port),"INBOX_ORIGIN":origin})
        try:
            for _ in range(100):
                try:
                    with urlopen(origin+"/api/health",timeout=1): break
                except OSError: time.sleep(.05)
            exercise(origin,root,(root/"setup-code").read_text())
        finally:
            proc.terminate()
            proc.wait(timeout=5)
