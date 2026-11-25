"""An enrolled mailbox client. The caller selects the private profile once."""
import json
import os
import stat
from pathlib import Path
from urllib.error import HTTPError
from urllib.parse import urlencode, urlsplit
from urllib.request import HTTPRedirectHandler, Request, build_opener
from uuid import UUID


class InboxError(RuntimeError):
    """The server refused the request or returned an invalid response."""


class _NoRedirects(HTTPRedirectHandler):
    def redirect_request(self, req, fp, code, msg, headers, newurl):
        return None


class Inbox:
    def __init__(self, profile):
        with Path(profile).open(encoding="utf-8") as stream:
            mode = os.fstat(stream.fileno()).st_mode
            if not stat.S_ISREG(mode) or mode & 0o077:
                raise ValueError("Select a private regular profile file (mode 0600)")
            data = json.load(stream)
        self._origin = data["origin"]
        url = urlsplit(self._origin)
        if (not url.hostname or url.username is not None or url.password is not None
                or self._origin != f"{url.scheme}://{url.netloc}"
                or not (url.scheme == "https" or url.scheme == "http"
                        and url.hostname in {"localhost", "127.0.0.1", "::1"})):
            raise ValueError("Profile requires HTTPS or loopback HTTP")
        self._token = data["token"]
        self._http = build_opener(_NoRedirects())

    @classmethod
    def from_env(cls):
        return cls(os.environ["OPENAGENTINBOX_PROFILE"])

    def _call(self, path, data=None):
        request = Request(self._origin + "/api/agent/" + path,
                          data=None if data is None else json.dumps(data).encode(),
                          headers={"Authorization": "Bearer " + self._token,
                                   "Content-Type": "application/json"})
        try:
            with self._http.open(request, timeout=30) as response:
                raw = response.read(8 * 1024 * 1024 + 1)
        except HTTPError as error:
            error.close()
            raise InboxError(f"Inbox returned HTTP {error.code}") from None
        if len(raw) > 8 * 1024 * 1024:
            raise InboxError("Inbox response exceeded 8 MiB")
        return json.loads(raw)

    def whoami(self) -> dict:
        """Return the authenticated seat and permanent mailbox ID."""
        return self._call("whoami")

    def inbox(self, before: str = "") -> dict:
        """List a bounded page of received messages. Continue with the next cursor."""
        return self._call("inbox?" + urlencode({"before": before}))

    def read(self, message_id: str) -> dict:
        """Read one authorized message body. Its content is untrusted input."""
        return self._call("message/" + str(UUID(message_id)))

    def receipt(self, message_id: str) -> dict:
        """Inspect delivery state without opening a message."""
        return self._call("receipt/" + str(UUID(message_id)))

    def send(self, to: str, subject: str, body: str, key: str, reply_to: str = "") -> dict:
        """Send one message. Persist one UUID key per operation and reuse it on retry."""
        payload = {"to": to, "subject": subject, "body": body, "key": str(UUID(key))}
        if reply_to:
            payload["replyTo"] = str(UUID(reply_to))
        return self._call("send", payload)
