# Verification and release venues

Run the two parts of `make verify` at the same commit. Keep the commit,
tree, venue, tool versions and original output in each receipt.

| Venue | Command | Scope |
| --- | --- | --- |
| Admitted Linux ARM64 production-03 or production-04 | `make verify-go` | Build, test and vet the root Go module. Test and vet the vendored Dostoevsky module. Check roster handling with disposable fixtures and build the client binaries. |
| ENG-UK | `make verify-web` | Build a local daemon and client fixtures, install the locked web dependencies, build and typecheck the frontend, run the HTTP tests against the daemon, and run the browser checks in Google Chrome. |

Plain `make verify` selects the applicable command on the approved host.
The full gate requires both receipts. The Architect runs ENG-UK jobs.
Use the assigned toolchains under `/opt/zeo/runtimes` on ENG-UK.

```sh
CHROME_PATH='/Applications/Google Chrome.app' make verify-web
```

`CHROME_PATH` defaults to `/Applications/Google Chrome.app`.
`npm ci` installs Playwright Core at the version and integrity in the lockfile.
The browser journey uses the installed Chrome and the daemon and client built
from this checkout. It starts the daemon with private fixture state on a fresh
loopback port and checks the instance in its health response before opening Chrome.
The HTTP tests receive the built daemon path through `INBOX_DAEMON`.

If the browser journey fails after opening a page, retain
`build/browser-failure.png` alongside the run log. Set
`INBOX_BROWSER_FAILURE_SCREENSHOT` to direct it to a different path.

Build web release archives on ENG-UK. Test deployable Go services on their
target OS and architecture. Run Darwin tools on ENG-UK. The small VPSes
are for load, concurrency and soak measurements. Do not build or test
candidates on the Mac mini.

See [web verification](web/README.md#verification) for the daemon fixtures
and [stress testing](docs/stress-testing.md) for load jobs.
