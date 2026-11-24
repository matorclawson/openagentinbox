# Replaying a mailbox workload

Use the stress runner on a disposable Linux host. Supply the export as a local JSONL file. Each row provides id, from, to, at, conversation, message_id, in_reply_to and body. The capture used for 0.12.2 has 10,000 chronological records and 73 distinct mailbox names. Commas separate recipients.

```bash
export GOMAXPROCS=2
bash tests/stress.sh -corpus "$HOME/Downloads/zeo-messages-10k.jsonl" \
  -seats 73 -workers 32 -rates 100,1000,10000 -scenarios mixed \
  -duration 1m -out steady-results.jsonl
bash tests/stress.sh -corpus "$HOME/Downloads/zeo-messages-10k.jsonl" \
  -seats 73 -workers 32 -rates 10000 -scenarios trace \
  -duration 1m -out trace-results.jsonl
```

Each case constructs a fresh database with historical sender/recipient distribution, fan-out, conversation groups, earlier linked parents, priority, body byte sizes and send times rounded to the minute. The harness substitutes synthetic text, identifiers and account names. Compressed storage and content-dependent processing can therefore differ from production. The seed uses batch insertion and the live workload uses authenticated HTTP. A run with an empty corpus or too few seats fails early.

In steady mode sends are equally spaced and deterministically sample message shapes across the capture. Trace mode preserves interarrival proportions and order, scaling the full capture into a chosen window. Choose a whole number of capture cycles. Each new workflow sends a single message to one sampled historical recipient, retrieves its body, lists a bounded thread page with previews every fourth send and retries the same idempotency key every tenth send. Historical fan-out and parent edges remain in the seeded store. New live sends exercise one recipient and independent roots. Compression, real SMTP, model wakes and attachment processing require separate measurements.

The default runner queue holds one pending job per worker. A full queue records a driver drop before submission. `-queue N` changes only the driver's waiting capacity. Compare scheduled, dropped, confirmed-in-window, drained confirmations and persisted counts together. Send and list percentiles are conservative histogram bucket bounds. Peak RSS is the daemon's high-water mark, excluding the driver. The process checks SQLite integrity, operation identities, route, body bytes and row totals after shutdown. Compare runs on the same storage and machine before drawing latency conclusions.

To run visual checks with local export content, install Python Playwright with Chromium, build the UI and daemon, and run:

```bash
python3 tests/corpus-layout.py ./dist/linux-amd64/openagentinboxd "$HOME/Downloads/zeo-messages-10k.jsonl" ./web/out
```

On ARM64 use the ARM64 binary. Set CHROMIUM to use an installed browser. This particular fixture expects the supplied header conventions and at least one P0 message. It exercises 41 actual bodies in a loopback-only temporary mailbox, checks narrow and desktop layouts, and prints only the test outcome. Keep the export outside the checkout.
