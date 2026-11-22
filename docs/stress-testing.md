# Measure OpenAgentInbox throughput on Linux

Run the same suite from a source checkout (Go 1.27) or the prebuilt Linux ARM64/AMD64 kit (Bash and getconf). The harness starts its own daemon on loopback, creates a private disposable SQLite database and generates its own owner and agent credentials. It never accepts an installation URL or an existing database. The machine's CPU, memory and filesystem still bear the full cost. TMPDIR selects the volume used for the test databases and snapshots.

## Run the matrix

```sh
bash tests/stress.sh -out matrix.jsonl
```

For 50 inboxes receiving 2, 20 and 200 messages per minute each, run the mixed scenario. The rates flag is the aggregate rate. The report includes the verified new message count for each recipient in `StoredByInbox`.

```sh
bash tests/stress.sh -seats 50 -scenarios mixed -rates 100,1000,10000 -duration 1m -out fifty-inboxes.jsonl
```

Defaults are 300 agents, 10,000 seeded historical messages, 512-byte new bodies and 32 concurrent workers. Each scenario runs at 100, 1,000 and 10,000 unique messages/minute for one minute per rate. Allow at least nine minutes for the default matrix. Each cell starts a new database and daemon. The initial mailbox history and account creation happen before the timed window. Each request uses an actual agent bearer credential. No owner request is used for measured delivery.

| Scenario | Workload |
|---|---|
| mixed | Sends spread across senders and recipients. Every send is followed by an authenticated body read. Every fourth send adds an inbox page read and every tenth send retries the identical key. |
| hot | The same operations, but every sender writes to one busy recipient. |
| maintenance | The mixed workload plus an online backup at the midpoint. The snapshot helper runs the production integrity and count checks. After the load drains the daemon shuts down and restarts on the same database. |
| burst | The same average schedule compressed into the first second of each five-second interval. Use a multiple of five seconds to measure complete cycles. |
| crash | After the timed workload drains, kill the daemon with SIGKILL, reopen it, replay a send and read the addressed message using its original agent credentials. |

Burst and crash are opt-in. Crash tests the durability of confirmed writes across process death. It does not inject a crash during a partially completed client request or simulate disk loss. Maintenance tests a snapshot and restart of the live store; a full off-host restore is a separate drill.

```sh
# Compare burst handling and abrupt restart with larger messages.
bash tests/stress.sh -scenarios burst,crash -rates 10000 -duration 1m \
  -body-bytes 4096 -out adverse.jsonl

# Compare a larger starting history.
bash tests/stress.sh -scenarios mixed -rates 1000 -history 223000 -out history.jsonl

# Four-hour memory-growth study at 100/min. Increase duration to fit the host.
bash tests/stress.sh -scenarios maintenance -rates 100 -duration 4h -out soak.jsonl
```

At 10,000/minute a 24-hour run creates 14.4 million new messages. Use actual database and snapshot growth from a short run on the same volume to budget disk space. The stress process removes its disposable data when the cell returns. The JSONL report remains. Supply a new report filename for each run.

## Read the report

There is one JSON object per cell. `Config.Duration` is in nanoseconds. Progress goes to stderr. Exit 0 means all workflows and persistence checks succeeded. Exit 1 indicates configuration or report I/O failure. Exit 2 means one or more cells failed; previous completed rows remain in the report.

- **Throughput.** `Scheduled` counts unique send operations offered. `ConfirmedInWindow` and `WindowRate` count successful send responses received before the measurement deadline. `Confirmed` includes responses during drain. `DrainedRate` includes drain time in the denominator. `Dropped` counts work the load generator could not dispatch, including a full bounded queue. `WorkflowErrors` also counts wrong IDs, bodies, senders or recipients. A slow cell can finish correctly while missing its in-window target; examine both throughput and latency.
- **Latency.** For each successful operation, `Latency` reports count, p50, p95, p99 and maximum in milliseconds. Percentiles use bounded logarithmic buckets with about 5% resolution. The maximum is observed directly. `send` measures the HTTP request. `scheduled_to_send` includes scheduler delay and queueing. `workflow_from_schedule` also includes read, list and retry work.
- **Daemon memory and CPU.** `BaselineRSSKiB`, `EndRSSKiB` and `PeakRSSKiB` come from Linux /proc. Peak is the kernel's process-lifetime RSS high-water mark and includes startup/provisioning. `CPUPercentOneCore` comes from user plus system CPU time over the measurement interval. 100% means one fully occupied core. `Samples` contains elapsed time, RSS, high-water mark, cumulative CPU ticks, and SQLite database/WAL file sizes at roughly one-second intervals. Daemon RSS excludes the generator and backup subprocess and is different from whole-machine memory or cgroup file cache. The current backup result also reports the helper's CPU seconds and peak RSS.
- **Correctness.** The harness validates returned message bodies, recipient scoping, retry IDs, total messages/deliveries, operation keys, and stored payloads. Each cell runs SQLite integrity_check. `Persisted` counts new verified messages. Failed requests and missing work remain visible even if SQLite itself is healthy.

For comparisons, use the same machine, disk, build, initial history and process affinity. Record architecture and available CPU/memory. Repeat cells in different orders. Run longer trials before drawing conclusions about leaks, long-run memory growth or hardware capacity.

For automated acceptance, add your own thresholds, for example `-min-window-percent 99 -max-send-p95 100ms -max-rss-mib 192`. These numbers are examples, not product guarantees. Set `-cooldown 60s` to collect another minute of idle RSS and CPU samples after draining the workload. Idle samples are reported separately so they do not dilute the load CPU percentage.
