# Embedded Dostoevsky in OpenAgentInbox 0.10.1

The application uses the canonical github.com/rodriveracom/dostoevsky module with an offline replacement in third_party/dostoevsky. The supplied upstream VERSION is 0.3.0 and its changelog identifies a candidate. The local v0.3.0-rc.0 requirement identifies this snapshot independently of public tags. Source archive SHA-256 is cf0ed72c1fd849b2eabcb9b95177e18a44ebfe1abf414d7060b46d5444755287. Library Go files, sketches, module metadata, tests and license come from that archive.

## What runs in production

The existing bounded, per-inbox notification channels and SQLite batch reader use the updated module. The candidate's keyed notification implementation matches the previous source. Notifications still follow committed writes and readers still check durable state after a wake.

Concurrent native, HTTP, web and MCP sends can share a SQLite commit. The first writer runs immediately. While it is writing, up to 32 additional sends with at most 1 MiB of combined body data queue for the next transaction. At most one extra batch waits behind the active batch. Overflow uses the existing SQLite writer. Each send has its own retry key, receipt callback and savepoint. Failure of a receipt rolls back that individual send. Failure to commit the containing transaction returns errors to all its callers. Wakes and successful responses follow commit. For ambiguous transport failures, retry with the original UUID.

Fetching an already opened message reads its body and receipt together. Repeated reads no longer issue a write. Concurrent first opens still record a single canonical timestamp. The operator and read-only delegates retain their existing receipt semantics.

Activity → Mail performance since restart reads an owner-only endpoint on demand. Two fixed 960-bucket histograms and one 1,024-register HyperLogLog use approximately 16 KiB of counter storage per daemon. Counts measure successful handler calls, including successful retries. The cardinality estimate measures distinct canonical recipient mailboxes addressed by these sends. An open means a successful body fetch, including a permitted operator or delegated read. The durable recipient-open receipt remains specific to the addressee. Histogram values are p95 upper bounds, in milliseconds, for handler time before HTTP serialization. Metrics reset on restart. No model or background collector participates.

The stress driver now uses the same histogram algorithm. Use the same driver for comparisons across releases. Its percentile upper bounds are clipped to the exact observed maximum.

## Durable storage boundary

Dostoevsky's journal PublishBatch can persist a prefix on an unsuccessful batch. OpenAgentInbox requires messages, deliveries, idempotency records, linked replies and deadlines to share a database transaction. SQLite provides that transaction. The new journal backup, retention, recovery and dynamic-topic features apply to deployments of the Dostoevsky journal. OpenAgentInbox continues to back up its authoritative SQLite database. Approximations are used only for telemetry.

## Verify

Run go test ./... and go vet ./... in the project root and in third_party/dostoevsky. Run the web tests with the new daemon and client assets. The release includes comparable JSONL measurements from 76 disposable inboxes with 10,000 historical messages each run. Use the stress driver's requests scenario for deadline workflows and burst for simultaneous arrivals. Production ARM64 disk latency and long-duration memory behavior need measurement on the target VPS.
