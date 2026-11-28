# What OpenAgentInbox must carry into Rust

The 0.12.14 channel council tree at `f4032cd033741eb74f6f307f446166c464f6c8c1` contains **77 first-party Go implementation files across 16 packages**, with 38 existing first-party Go test files at that revision. Five implementation files import either infrastructure library directly. All five implement mailbox-specific behavior around the library. Replacing the two libraries alone leaves the daemon, client, persistence and operations tools in Go.

The inventory counts tracked .go files in cmd/, delivery/ and internal/. It excludes seven upstream engine files in third_party/ephemeral, module-cache and vendor files, and all non-Go code. “Owned” means this repository owns compatibility and maintenance; it does not mean every algorithm is unique. TypeScript UI, JavaScript CLI compatibility, Python integrations and deployment shell scripts remain callers of the same product contracts.

## Ownership by package

| Package | Implementation files | Product responsibility that survives the port | Evidence to preserve |
|---|---:|---|---|
| cmd/openagentinboxd | 1 | Serve, snapshot and deploy entry points; daemon startup and exit conventions | New binary suite launches serve and uses deploy plan. Existing deployment and upgrade tests cover packaging. |
| internal/console | 25 | Owner sessions, delegated sign-in, agent credentials, pairing and enrollment, rename/aliases, web/agent/MCP handlers, inbox/sent, threads and search, request deadlines, escalation, probes, live updates, email identity, inbound mail and relay reconciliation | New product journeys and email ancestry test; existing tests/email-edge.py, web/tests and internal/console tests |
| internal/deliverystore | 25 | SQLite schema and migrations, operation-key replay, commit batching, audience, reads and acknowledgement semantics, counters and pages, archive import/export, change feed and retention, activity, digests, obligations and wake policy | New fingerprint vectors and cutover journeys; store, native, scaling, reliable, batch, pending and import/restore tests |
| delivery | 3 | Public, body-free Runtime envelope and receipt protocol; authority-checked receiving journal. Runtime provides assignment authority and executes the agent. | receiver_test.go and http_test.go, including replay after assignment replacement |
| cmd/openagentinbox-client | 7 | Seat-scoped profile, CLI/MCP, roster onboarding, focus, muted/idle notification handling, probe confirmations, background worker definitions | New CLI/MCP permission journey; existing notify, probe, worker and harness tests |
| internal/mcprpc | 1 | Strict RPC JSON, schemas, negotiated versions and dispatch shared by client/server | Exercise through real MCP in the new suite and tests/harnesses.py; preserve parser rejection cases during port |
| internal/deploy | 1 | Machine-readable plan/apply/status, local or SSH setup, health checks | New no-side-effect plan contract, deploy_test.go and disposable installer rehearsal |
| cmd/openagentinbox-store-admin | 1 | Offline operator CLI for registration, fence operations and rename impact | New exact-reason fence contract; seat registration and overlap need additional binary parity cases |
| cmd/openagentinbox-backup | 2 | Snapshot/verify command and backup/restore workflow invocation and receipts | New online snapshot/restore journey; internal/backupflow tests |
| internal/sqlitebackup | 1 | Consistent private snapshot, integrity check, no overwrite, manifest, streaming upload | New snapshot journey; backup_test.go |
| internal/offsite | 1 | Ciphertext upload/readback and size/hash evidence | store_test.go |
| internal/backupflow | 2 | Source, identity, encryption recipient, artifact lifecycle, operation binding, restore evidence and verified cleanup | flow_test.go covers replay, uncertain upload and corruption. Qualify real age encryption and offsite transport separately. |
| internal/channelbinding | 1 | Operator-approved external subject mapping, room/seat membership and disclosure per audience revision | binding_test.go and normalized channel journeys |
| internal/channelqueue | 1 | Installation-specific transactional outbox, attempt fencing, ambiguous handoff, reconciliation evidence | queue_test.go and normalized channel journeys |
| internal/channelrequest | 1 | Assignment revision, takeover, handoff, deadline, escalation once per assignment | request_test.go |
| cmd/openagentinbox-stress | 4 | Local workload driver, corpus shaping, load-window/persistence accounting and RSS reporting | corpus_test.go, workload_test.go and tests/stress.sh; port or retain a language-neutral benchmark before retiring Go |

The three channel packages are reviewed candidate code. Their SQL and transitions have tests, but the production daemon does not install their schemas on startup. Preserve the distinction between ordinary mailbox receipts, delivery to Runtime, and external transport delivery. Each proves a different event.

## The five direct dependencies

| Caller | Upstream facility | What OpenAgentInbox owns |
|---|---|---|
| internal/deliverystore/native_watch.go | Dostoevsky keyed Notifications | Seat/probe keying, commit-before-signal, subscription lifetime, 8 waiters per seat, 64 native waiters and 512 total; reopening a mailbox must discover committed data even when the wake was missed. |
| internal/deliverystore/unread_batch.go | Dostoevsky BatchReader, Page, Limits and records | Authenticated seat, SQLite query, unread topic and sparse exclusive cursor; 50-record / 2 MiB limits, exact byte charging, body-free projection and empty/oversize behavior. |
| internal/console/performance.go | Dostoevsky latency histogram and HyperLogLog | Successful send/read observation points, process lifetime, recipient dimension and diagnostic fields. An estimated recipient count can never authorize access or settle a receipt. |
| cmd/openagentinbox-stress/workload.go | Dostoevsky latency histogram | Offered vs persisted workload, stage timings, bounded scheduling, throughput and memory reporting. Preserve histogram upper bounds versus exact latency when comparing results. |
| internal/backupflow/flow.go | Ephemeral workflow store, StartOnce, registration, tasks and durable execution | Backup and restore step definitions, stable run identity, hash of input and configuration, pinned encryption destination, receipts and filesystem cleanup rules. |

Database/sql, the SQLite driver, UUIDs, crypto and the Go standard library are additional dependencies. No general-purpose Go API or concurrency primitive needs a line-for-line Rust counterpart. Existing SQLite files, authority boundaries, public interfaces and durable outcomes do need compatible readers and writers.

## Run the independent product tests

The Python standard-library driver executes installed **binaries**. It imports no Go package, inference SDK, workflow engine or streaming library. Tests use a new temporary home and loopback instance each time. A future Rust binary can take the same place under each executable name. No live accounts, provider credentials, internet connection or configured service are needed for the runner.

Build today’s reference on an admitted host, then run:

```sh
mkdir -p build/product-reference
go build -o "$PWD/build/product-reference/" ./cmd/...
make verify-product-contracts OAI_BIN_DIR="$PWD/build/product-reference"
```

For a migration rehearsal, supply the prior daemon and the proposed daemon/client/admin/backup tools in separate directories:

```sh
make verify-product-contracts OAI_REFERENCE_BIN_DIR=/absolute/reference/bin OAI_BIN_DIR=/absolute/candidate/bin
```

With both directories supplied the reference daemon provisions the owner, credentials and fixture data. The candidate either opens the existing store, opens its online snapshot, or runs the requested administrative operation against it. Also run with just OAI_BIN_DIR to exercise candidate-native setup. The runner requires the names openagentinboxd, openagentinbox-client, openagentinbox-store-admin and openagentinbox-backup in the candidate directory; the reference directory needs only openagentinboxd.

### What fails a replacement

1. One request retried in parallel after restart produces two messages, a different payload can reuse its key, or a wrong recipient can receive a reply.
2. Listing, searching, inspecting a receipt, reading as sender or polling for a notice changes the recipient's opened timestamp. A recipient's repeated read must preserve its first open. A linked reply must resolve the request.
3. An unrelated seat can read or discover a private message, use another profile through an explicit seat selection, or get more authority over MCP than over HTTP.
4. A rename preview, or a batch with a later conflicting rename, partially changes the roster. A valid rename must retain mailbox ID, history and idempotent replay via the new alias. Rotating credentials must invalidate the predecessor.
5. A saved page cursor skips old rows or repeats rows across cutover and a new arrival. A mailbox notice exposes content or includes a different seat's traffic.
6. A local seat can send internet email by default, or a refused external send consumes the retry key or creates a sent item.
7. An online snapshot overwrites an existing destination, drops credentials or receipts, loses operation-key replay, or contains messages accepted after the snapshot. Verify its manifest counts, file permissions and SHA-256 before reopening it.
8. An offline fence can be cleared using the wrong reason, or a local deploy plan creates its target directory.

The new Go email test covers same correspondent and inbox, different inbox, different correspondent, same subject only, and a reused remote Message-ID. A reply with an ambiguous reference must stay in its own thread. The test calls the real email acceptance path. Re-express those cases through LMTP when qualifying the Rust daemon.

The seven language-neutral fixtures in internal/deliverystore/testdata/submission-digests.json pin the persisted operations digest. Body and ExternalPayloadDigest are base64 bytes; use UTF-8 byte counts for strings. Hash length-prefixed fields in order: sender, conversation ID, parent ID, subject, decimal priority, then the recipient count with its r prefix, each ordered recipient, body and optional markers for final, intent and transport digest. A field is decimal byte length, ASCII colon and exact bytes. Go’s generated message ID and transport-derived ThreadHint are excluded. Rust must match the full hex SHA-256 for every vector. The live replay tests also verify the effect of that digest when opening an existing store.

## Cutover evidence still needed

The portable suite is a first parity gate. It exercises four binaries and does not cover every route or internal state. Retain the Go package tests as a behavioral reference until equivalent Rust cases run. In particular, qualify old-schema import/export, Runtime assignment replacement, focus/mute timers and listener restart, all email relay failure states, offsite restore, channel takeover after restart, full browser and CLI onboarding, and stress-driver accounting. Use existing integration fixtures as inputs where possible. Verify real hardware latency/RSS and mailbox counts with matched datasets and rates; unit-test success alone does not establish performance parity.

Test a forward upgrade using a snapshot from an actual supported release and the packaged installer rollback on a disposable host. Check accepted writes and uncertain external deliveries before considering rollback. Remove Go from release builds only after the remaining binaries, CI/release scripts, runtime dependencies and module references are accounted for. A Rust infrastructure library alone cannot retire the mailbox daemon.
