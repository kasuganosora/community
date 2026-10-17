# Benchmarks

Measured on **2026-10-17 (UTC+8)** on a shared 8-core / 15 GB Linux build box, Docker 26.1.5+dfsg1 (cgroup v2), every container with a hard CPU and memory limit and **`--memory-swap` equal to `--memory`** (no swap). Community was built from `master` at `bf24e33` (static Go binary, `CGO_ENABLED=0`). Scripts: [`scripts/bench/`](../scripts/bench/). Raw output: [`benchmarks-raw/`](benchmarks-raw/).

## Workload

[`scripts/bench/load.py`](../scripts/bench/load.py): 10 local users (created with `tootctl`), each following 5 others, 8 concurrent clients for **10 minutes** using the Mastodon client API with OAuth tokens:

| Operation | Share |
| --- | --- |
| Home timeline (`GET /api/v1/timelines/home`) | 45% |
| Public timeline | 15% |
| New post | 15% |
| New poll | 5% |
| Media upload (300 KB JPEG via `/api/v2/media`) + post | 5% |
| Favourite a recent post | 10% |
| Notifications | 5% |

The load generator ran on the same host (outside the limits). Federation was not exercised (no remote servers). The setup wizard was not used: configuration was passed as environment variables.

Memory columns: **peak** is the cgroup `memory.peak` (includes page cache, e.g. SQLite/PostgreSQL files and uploaded media); **RSS** is the process resident set or `docker stats` usage (excludes reclaimable cache).

## Minimum tier: 1 CPU / 1 GB, SQLite + Redis, no Meilisearch

One container (`redis:7-alpine` running `redis-server` + `community`) with `--cpus=1 --memory=1g --memory-swap=1g`.

| | Value |
| --- | --- |
| Community RSS idle (after start, 10 users) | 78 MiB |
| Community RSS peak under load | 196 MiB |
| Redis RSS peak | 9.4 MiB |
| cgroup peak | 1024 MiB (hit the limit; at the end 153 MiB anonymous, 816 MiB page cache, reclaimed as needed) |
| OOM kills | **0** |
| Throughput | 97 req/s (58,281 ok, 99 errors) |
| Latency p50 / p95 (all requests) | 71 ms / 198 ms |

| op | ok | err | p50 ms | p95 ms |
| --- | --- | --- | --- | --- |
| home | 26396 | 0 | 66 | 157 |
| public | 8695 | 0 | 68 | 160 |
| post | 8722 | 0 | 62 | 124 |
| poll | 2947 | 0 | 65 | 135 |
| media | 2896 | 0 | 129 | 234 |
| favourite | 5691 | 99 | 74 | 162 |
| notifications | 2934 | 0 | 216 | 399 |

The favourite errors are the load generator re-favouriting posts (expected API errors, not server failures).

## Recommended tier: PostgreSQL 17 + Redis 7 + Meilisearch 1.12 + Community

Four containers (host networking), total 2 CPUs: Community 1.0, PostgreSQL 0.5, Meilisearch 0.3, Redis 0.2.

| Memory caps | 2 GB total | 4 GB total |
| --- | --- | --- |
| Community | 640 MiB | 1280 MiB |
| PostgreSQL | 512 MiB | 1024 MiB |
| Meilisearch | 704 MiB | 1408 MiB |
| Redis | 192 MiB | 384 MiB |

| Result | 2 GB | 4 GB |
| --- | --- | --- |
| Community idle / max RSS / cgroup peak | 19 / 69 / 250 MiB | 19 / 71 / 215 MiB |
| PostgreSQL max RSS / cgroup peak | 173 / 300 MiB | 187 / 297 MiB |
| Redis cgroup peak | 30 MiB | 27 MiB |
| Meilisearch max RSS / cgroup peak | 193 MiB, then **OOM-killed** (~6 min in) | 663 / 733 MiB |
| OOM kills | **1 (Meilisearch)**; Community kept serving, search falls back to the database | 0 |
| Throughput | 18.6 req/s | 15.9 req/s |
| Latency p50 / p95 | 305 / 1092 ms | 399 / 1209 ms |

### Reading these numbers

- **Meilisearch needs ~1 GB of headroom** once it indexes a steady stream of posts. A 704 MiB cap was not enough; at 1408 MiB it peaked at 733 MiB. On the live demo it uses ~932 MiB. That is why it is optional and why the minimum tier drops it.
- **Throughput in the recommended runs was CPU-bound on PostgreSQL** (pinned at its 0.5 CPU cap, ~52%), not on memory. It is not a statement that PostgreSQL is slower than SQLite in general; give PostgreSQL at least one full core for real deployments. The minimum tier gave the whole core to one container, which suited this small write-heavy test.
- Community itself stayed between ~70 and ~250 MiB in every run.

## Not measured yet (TODO)

- Ingesting federated traffic (signed inbox deliveries from remote servers). The SSRF guard refuses to fetch actor keys from private addresses, so a local fake remote server would need a public address; run this on a real staging host.
- A real 1 vCPU / 1 GB VPS (these were Docker limits on a larger host; disk and CPU steal differ).
- Larger user counts.

## Rerunning

```bash
# from the repository root
export BENCH=/tmp/community-bench
CGO_ENABLED=0 go build -o $BENCH/community ./cmd/community
CGO_ENABLED=0 go build -o $BENCH/tootctl ./cmd/tootctl
npm --prefix frontend ci && npm --prefix frontend run build
scripts/bench/run-minimum.sh
scripts/bench/run-recommended.sh 2g
scripts/bench/run-recommended.sh 4g
```

Set `DUR=60` for a quick run. The scripts remove their containers when done.
