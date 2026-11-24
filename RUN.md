# Verification and release venues

Run the two parts of `make verify` at the same commit. Keep the commit,
tree, venue, tool versions and original output in each receipt.

| Venue | Command | Scope |
| --- | --- | --- |
| Admitted Linux ARM64 production-03 or production-04 | `make verify-go` | Build, test and vet the root Go module. Test and vet the vendored Dostoevsky module. Check roster handling with disposable fixtures and build the client binaries. |
| ENG-UK | `make verify-web` | Build a local daemon and client fixtures, install the locked web dependencies, build and typecheck the frontend, run the HTTP tests against the daemon, and run the browser checks in Google Chrome. |

Plain `make verify` selects the applicable command on the approved host.
The full gate requires both receipts. The Architect runs ENG-UK jobs.
Use the assigned toolchains under `/opt/zeo/runtimes` on ENG-UK. Set
`PLAYWRIGHT_MODULE` to the absolute path of the admitted Playwright or
Playwright Core module on that host and record its version in the receipt.
The gate checks that the module loads before building. It uses the installed
Google Chrome, with no browser download.

```sh
PLAYWRIGHT_MODULE=/absolute/path/to/node_modules/playwright \
  CHROME_PATH='/Applications/Google Chrome.app' make verify-web
```

`CHROME_PATH` defaults to `/Applications/Google Chrome.app`. The gate sets
`INBOX_DAEMON` to the daemon built from the checked-out source.

Build web release archives on ENG-UK. Test deployable Go services on their
target OS and architecture. Run Darwin tools on ENG-UK. The small VPSes
are for load, concurrency and soak measurements. Do not build or test
candidates on the Mac mini.

See [web verification](web/README.md#verification) for the daemon fixtures
and [stress testing](docs/stress-testing.md) for load jobs.
