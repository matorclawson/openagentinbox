# Verification and release venues

Run the two parts of `make verify` at the same commit. Keep the commit,
tree, venue, tool versions and original output in each receipt.

| Venue | Command | Scope |
| --- | --- | --- |
| Admitted Linux ARM64 production-03 or production-04 | `make verify-go` | Build, test and vet the root Go module. Test and vet the vendored Dostoevsky module. Check roster handling with disposable fixtures and build the client binaries. |
| Admitted Linux ARM64 production-03 or production-04 | `make verify-harness` | Run the harness integration checks (native stdio and HTTP MCP, the Python client) against a daemon and client built from this checkout. Needs the Python MCP SDK; see the Makefile comment. |
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

## Release artifacts

`make release` builds the artifacts on ENG-UK into a new directory `build/release-COMMIT`: the
installer `openagentinbox-install-VERSION.sh`, `openagentinbox-VERSION-source.zip`, the clients
for linux-arm64, linux-amd64 and darwin-arm64, the Python wheel, `MANIFEST.json` and
`SHA256SUMS`. It prints the toolchain versions, the commit and every checksum.

The build never touches the working tree and never deletes a path it was given. It refuses a
dirty tree (there is no override), exports the exact commit with `git archive` into a
temporary directory, and builds there with `npm ci` from the lockfile, `GOTOOLCHAIN=local` and
read-only Go modules. The output directory must be new or empty and inside `build/`
(`tests/release-outdir.sh`, part of `make verify-go`, checks the guard); remove an old build
yourself before reusing its name. `MANIFEST.json` records the commit, the tree, the
toolchain versions, the lockfile and `go.sum` hashes and each file's sha256, with no time or
host name. Archive times come from the commit time, the Go binaries use `-trimpath` and no
build id, and the Next build id is the commit. `make release-check` builds twice and fails
unless both `SHA256SUMS` match, so a reproducibility claim needs a receipt from it. Publish the
files, `MANIFEST.json` and `SHA256SUMS` unchanged with the tag.

Build web release archives on ENG-UK. Test deployable Go services on their
target OS and architecture. Run Darwin tools on ENG-UK. The small VPSes
are for load, concurrency and soak measurements. Do not build or test
candidates on the Mac mini.

See [web verification](web/README.md#verification) for the daemon fixtures
and [stress testing](docs/stress-testing.md) for load jobs.
