# Feed reliability and open-arrival benchmarks

These tools implement measurement plumbing for [the feed scaling plan](../docs/tasks/feed_scaling_plan.md). They do not declare P0–P4 complete, create production accounts, alter ACLs, flush shared Redis, or restart services automatically.

Freeze the exact committed source and build a fresh baseline binary. Record commit, binary SHA-256, Go version, settings, process limits, topology and fixture hashes. An already running development binary is not a valid baseline unless its provenance matches. Preserve baseline artifacts when later tests fail or fixtures change.

## Read admission and latency

`benchmark-feed-open.py` uses scheduled arrivals with a bounded in-flight budget, rather than a request-after-response loop. Every intended arrival is recorded, including scheduler-late and in-flight-capacity drops. Reports distinguish offered/sent/valid requests per second, HTTP errors, semantic errors, arrival success ratio, service latency, send lag and scheduled-to-completion latency. A full response is read before completion is recorded.

A scenario contains a credential-free `base_url` and cases with `name`, `path`, `rate`, `limit`, optional `max_id`, `expected_ids` and `expected_signature`. Build those expectations from an independent SQL authorization/pagination oracle; first verify response metadata against SQL before freezing its normalized signature. The signature includes root/original/quote identity and visibility, content digest, media identity/type, polls and emoji shortcodes. It does not itself prove authorization.

```sh
python3 scripts/benchmark-feed-open.py \
  --scenario /tmp/feed-qa/read-scenario.json \
  --token-file /tmp/feed-qa/token \
  --output /tmp/feed-qa/baseline-read \
  --max-inflight 32 --warmup-seconds 120 --seconds 600 --repeats 3
```

The token file must be private. The output directory must not already exist. Keep cases fixed across baseline/candidate runs and retain raw JSONL. The default requires exact oracle IDs and metadata signatures. `--structural-only` is explicitly unqualified smoke testing; never use it to claim no leakage or no missing posts. Reduced duration/repetitions are smoke checks, not the plan's performance gate.

## Complete fanout, not first-recipient delivery

`benchmark-feed-fanout.py` publishes isolated public fixtures and checks that **every** expected recipient Redis candidate set contains the new ID. Read-path SQL fallback is not evidence of fanout completion. Prepare genuinely accepted local recipients, distinguish active/reading/inactive/remote followers, and save their exact IDs and relationships independently.

```sh
python3 scripts/benchmark-feed-fanout.py \
  --base-url http://127.0.0.1:18091 --token-file /tmp/feed-qa/author-token \
  --receivers /tmp/feed-qa/receivers.json --redis-port 16379 --redis-db 1 \
  --key-template 'feed:v3:{{home:{id}}}:items' \
  --posts 20 --publishers 2 --worker-processes 1 --label candidate-w1 \
  --completion-command /tmp/feed-qa/completion-argv.json \
  --output /tmp/feed-qa/candidate-w1
```

Python format escaping requires doubled literal braces for v3 keys. The old baseline uses `feed:home:{id}`. The Redis connection is intentionally an isolated unauthenticated loopback lab instance; this helper is not a production Redis administration client.

The optional completion command is a JSON argument array, not shell code. It receives the status ID and emits `{"done":true,"failed":0}` only when the corresponding committed event and every batch are complete. When configured, SQL completion is polled before a final complete-recipient Redis scan. Without it, the report explicitly marks durable completion unverified (appropriate for a pre-queue baseline only).

Record observer Redis commands and polling interval: the observer consumes resources and introduces timing uncertainty. The default write mode runs bounded **closed-loop batches**; it is useful for full-recipient latency and isolated 1/2/4-worker comparisons, not proof of open-arrival write capacity. Use `--arrival-rate` for the open-arrival mode described below. Use identical dependency/CPU budgets, datasets, observer settings and publisher counts. A 1,000-recipient result must not be extrapolated to 10,000 or 100,000. Do not interpret `worker_processes` metadata as proof that the correct processes actually ran; retain process/config snapshots.

## Fault windows

`benchmark-feed-fault.py` requires explicit PIDs and a matching executable path under `/tmp`. It records monotonic and wall-clock signal times. `pause` sends SIGSTOP and resumes in `finally`; `crash` sends SIGKILL and leaves restart to the orchestrator. It does not accept arbitrary shell commands or target existing production Docker containers.

Run independent sustained publish/read probes during a 60-second isolated Redis pause and inspect HTTP failures, durable queue age, bounded resource use, resumed fanout and SQL-oracle convergence. For worker crashes, keep other workers running, restart the failed process, wait for lease takeover and verify all recipients. A fault tool's successful exit says nothing about recovery: save the workload and final oracle evidence separately. Never kill the fault orchestrator while it has a paused target.

Additional mandatory plan cases (transaction rollback, ack-boundary crashes, stale fences, epoch reset, ACL races, multi-API cold rebuild and real Redis Cluster failover) require deterministic targeted tests or explicit fault injection; these generic tools do not fabricate that coverage.

## Self-check

```sh
PYTHONDONTWRITEBYTECODE=1 python3 -m unittest discover -s scripts -p test_benchmark_feed.py -v
```

The suite checks oracle mismatches, embedded content signatures, cursor/duplicate rejection and admission-drop accounting. Preserve separate labels for executed, failed, blocked and unexecuted hardware/scaling gates.

For fixed open-arrival writes, set `benchmark-feed-fanout.py --arrival-rate N`.
`--posts` is the intended arrival count; `--publishers` bounds concurrently
outstanding publication-plus-completion observers. Arrivals without a free slot,
or arriving after the scheduling tolerance, remain explicit failed samples;
they are never queued silently. Reports include the offered rate, dropped count,
and successful-arrival ratio. Choose and freeze both controls before comparing
worker counts. Completion observers themselves query SQL and Redis, so their
CPU and query counts must be included in the measurement limitations. Omitting
`--arrival-rate` retains the explicitly labeled closed-loop diagnostic mode.

## Browser fixtures

Before timing scroll runs, verify original media and thumbnail URLs through the
real API, decode the bytes, and compare MIME, dimensions and size with seeded
metadata. A zero JavaScript-error count does not establish that images loaded.
Reject failed same-origin assets/media responses and broken visible images;
do not substitute synthetic successful media responses through browser routing.
When using frozen UI builds, route only their actual files and document entry
point, leaving API and media requests on the real backend. Record exact loaded
counts at the 2,000/5,000 checkpoints, since pagination can prefetch another page.
Preserve aborted or invalid fixture runs separately from the final measurements.

HTTP keep-alive connections are recycled **before** a request when the monotonic
idle time since the last response body completed exceeds
`--connection-max-idle` (default30 seconds; finite positive values only). This
avoids reusing spare worker connections after the server's idle timeout during
an outage/recovery concurrency change. Raw samples record `connection_idle_s`
and `connection_recycled`; summaries count `connections_recycled`. Opening the
replacement connection remains inside service and scheduled-completion latency.
A failed request is never retried: genuine disconnects, timeouts and non-200
responses still count against valid throughput. Choose an idle budget below the
server's configured idle timeout; it is a client connection-lifecycle setting,
not a relaxed response deadline.
