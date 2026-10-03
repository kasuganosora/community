# Matrix client API benchmark

Requires Python 3 standard library. Use **dedicated test accounts/devices** and coordinate load with the environment owner. `/sync` is stateful even though it uses GET: it can change presence and deliver, consume or acknowledge to-device messages. Do not run this tool with an everyday Element session or a device receiving encryption keys. The tool sets `set_presence=offline` and sync `timeout=0`; these settings do not make sync side-effect-free.

Store a Matrix access token in a private external file (`chmod 600`) or `MATRIX_BENCH_TOKEN`. A private JSON fixture may supply `token`, `room_id`, `since`, `changed_since`, and `from`. Do not commit credentials. `MATRIX_BENCH_URL` supplies the default URL; HTTPS certificate validation remains enabled.

```sh
python3 scripts/benchmark-matrix.py --url http://localhost:8080 \
  --token-file /secure/path/matrix-token \
  --cases initial --concurrency 1 8 32 --duration 3 \
  --timeline-limit 20 --min-events 1 --output /tmp/matrix-initial.json
```

Use the **complete opaque** `next_batch` and messages `end` values when preparing the fixture, including suffixes. Do not strip a suffix or reconstruct tokens as timestamps. Keep static-data cursors fixed for a before/after comparison. `changed_since` must represent known pending changes; `since` for the idle case should represent an already-consumed, stable timeline. Test sends/longpoll/ACK behavior separately on isolated mutation accounts so writes do not change the measured dataset.

```sh
python3 scripts/benchmark-matrix.py --url http://localhost:8080 \
  --fixture /secure/path/matrix-fixture.json \
  --cases idle changed messages messages-deep \
  --concurrency 1 8 32 --duration 3 --limit 20 \
  --output /tmp/matrix-results.json
```

Room/cursor fields can instead be passed through `--room-id`, `--since`, `--changed-since`, and `--from-token`. Tokens are never printed or accepted as command-line bearer-token arguments. Cursors are passed intact to the server. `--requests-per-worker N` replaces the duration loop with a fixed request count, useful for large-room accounts. Start large-room workloads at concurrency 1 to estimate cost before increasing load.

The JSON report includes p50/p95/p99 (nearest rank), RPS, client CPU, payload bytes, event/room counts, HTTP and structural errors, and normalized response signatures. Signatures omit transport cursors and `unsigned.age`, retain event contents/state, and are evidence for equivalence, not a complete authorization oracle. Structural checks detect malformed/duplicate IDs, oversized message pages, unexpected timeline events in the idle scenario, and empty changed responses. `--min-events` makes an expected nonempty initial/messages workload fail when empty. It does not apply to idle. Exit 1 denotes HTTP/transport, validation or zero-request failure. The script does not validate room membership, historical visibility or receipt privacy; separate protocol tests must do that.

This is a closed-loop workload with one persistent HTTP connection per worker. HTTP latency covers request through complete response; JSON validation/signature work is excluded from that latency but included in wall time/RPS and client CPU. Inspect client CPU before attributing a throughput ceiling to the server. No warmup is applied automatically. In-flight requests can extend elapsed time beyond duration by up to the request timeout. Tail percentiles from very few requests are not reliable; report medians/ranges for fixed small samples. Keep server CPU/pool/logging/schema and fixtures unchanged, and compare equivalent successful responses. The tool does not seed data, flush caches, send room messages or restart services.
