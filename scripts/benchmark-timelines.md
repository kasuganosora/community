# Timeline HTTP benchmark

Requires Python 3 standard library. Run against an environment authorized for load testing; defaults to one worker and a 3-second public timeline sample. The script only sends GET requests: it does not seed data, flush caches, or manage services.

```sh
python3 scripts/benchmark-timelines.py --url http://localhost:8080 \
  --cases public --concurrency 1 8 32 --limit 20 40 --duration 3 \
  --output /tmp/timeline-results.json
```

For authenticated home/user/list/tag scenarios, create an external JSON object containing `token`, `user_id`, `list_id`, and `tag`, then `chmod 600` that file. Keep it outside the checkout. Omit unneeded fields. The tool rejects credential files readable by group/others.

```sh
python3 scripts/benchmark-timelines.py --url http://localhost:8080 \
  --fixture /secure/path/timeline-fixture.json \
  --cases home user list tag --concurrency 1 8 --limit 20 --duration 3 \
  --output /tmp/authenticated-timeline-results.json
```

Alternatively supply `TIMELINE_BENCH_TOKEN` or a private `--token-file`; endpoint selectors also accept `--user-id`, `--list-id`, `--tag`. `TIMELINE_BENCH_URL` sets the default base. Tokens are never included in reports or command arguments. HTTPS certificate validation remains enabled.

`--max-id ID` benchmarks a deep page and checks that returned IDs precede the exclusive cursor. Checks also detect duplicate/malformed status IDs, non-descending order, non-array JSON, and oversized pages. Exit 1 means HTTP/transport or structural validation errors. The script deliberately does **not** infer business authorization, list membership, visibility, or expected page fullness. Empty pages appear in `item_counts`; do not mistake them for useful throughput.

Each case/limit/concurrency combination runs for the specified duration. Results include nearest-rank p50/p95/p99 latency, requests/second, status/error counts, returned item counts, and response bytes. This is a closed-loop workload with one persistent HTTP connection per worker and no coordinated-omission correction. Latency covers request through full response; elapsed throughput includes parsing/validation. In-flight requests may extend wall time by the request timeout (default 20s). No warmup or cache changes occur: establish and record cache state externally, keep database/CPU/logging/pool settings stable, and compare equivalent valid responses. A single cold request is not a cold-cache concurrent benchmark.
