# Meilisearch operations

Community uses Meilisearch for full-text search (`accounts`, `statuses`,
`hashtags` indexes, maintained by `search/indexsync`) and, when
recommendations are in `auto` mode, for the discovery index
(`discovery_statuses_v1_*`, rebuilt by `discovery/meili_provider.go`).
Search always falls back to the database when the engine is unavailable.

## How index writes reach Meilisearch

Incremental changes go through an in-process batching queue
(`search/index_queue.go`):

- Status create/update/delete events, profile edits and the tags of a new
  status enqueue an upsert or delete. The handler never waits for
  Meilisearch.
- The queue keeps only the latest operation per (index, document id): an
  update followed by a delete sends only the delete; ten edits of one post
  send one document.
- Every `SEARCH_INDEX_FLUSH_INTERVAL_MS` (default 3000), or as soon as one
  index has `SEARCH_INDEX_BATCH_MAX_DOCS` (500) documents or
  `SEARCH_INDEX_BATCH_MAX_BYTES` (5 MiB) queued, it sends one
  `POST /indexes/<uid>/documents` and/or one
  `POST /indexes/<uid>/documents/delete-batch` per index.
- Task results are checked with a single `GET /tasks?uids=...` for all
  outstanding batches, at most once per
  `SEARCH_INDEX_TASK_CHECK_INTERVAL_MS` (default 5000, never below 1000).
  Failed tasks are logged and retried in halves (so one bad document cannot
  fail the rest) with exponential backoff (1 s doubling to 60 s), up to
  `SEARCH_INDEX_MAX_RETRIES` (5) attempts. Transport errors and 5xx back off
  the whole queue the same way. A retry never overwrites a newer version of
  the same document.
- At most `SEARCH_INDEX_QUEUE_MAX` (50000) documents are held; beyond that new
  documents are dropped with a rate-limited warning.

Before this queue, every change was its own Meilisearch task (one LMDB commit
each) and the app polled `GET /tasks/<uid>` every 50 ms until it finished,
which made Meilisearch most of the disk writes on spinning disks and starved
PostgreSQL's fsyncs.

### Tradeoff and durability

- New or edited content becomes searchable after the flush interval plus
  Meilisearch's own indexing time (typically 3-5 s instead of under 1 s).
- Queued writes live in memory. A graceful shutdown (SIGTERM) sends them
  before the process exits. A crash or `kill -9` loses at most one flush
  interval of changes. Once Meilisearch has answered 202 the batch is in its
  own durable task queue.
- Lost upserts are repaired by the full backfill that `search/indexsync` runs
  whenever the engine (re)connects, which includes every process start
  (progress: `progress stage=search-index/...` log lines and
  the `search_index` field of the admin discovery status shown on /admin/discovery). A lost delete leaves a
  stale document in the index, but search results are re-read from the
  database, so a deleted post is never shown.

The backfill itself sends `SEARCH_REINDEX_BATCH_SIZE` (100) rows per request,
checkpoints only batches Meilisearch has acknowledged, and waits for each
batch's task with backoff (100 ms doubling to 1 s).

## Recommended container settings

For the Meilisearch container, use:

```yaml
environment:
  MEILI_ENV: production
  MEILI_LOG_LEVEL: WARN
  MEILI_NO_ANALYTICS: "true"
```

- `MEILI_ENV=production` requires a master key of at least 16 bytes
  (`MEILISEARCH_API_KEY`) and disables the development web UI.
- `MEILI_LOG_LEVEL=WARN` stops the per-request INFO log lines, which are
  extra writes on the same disk.

The bundled `docker-compose.yml` reads `MEILI_ENV` (default `development`)
and `MEILI_LOG_LEVEL` (default `INFO`) from the compose `.env`, so upgrading
never changes the mode by itself. To opt in, set `MEILI_ENV=production` (with a
`MEILISEARCH_API_KEY` of at least 16 bytes) and `MEILI_LOG_LEVEL=WARN`, then
recreate the container: `docker compose up -d --force-recreate meilisearch`.

These settings only take effect when the container is recreated:

```sh
docker compose up -d --force-recreate meilisearch
docker compose logs --tail=50 meilisearch   # no per-request lines; check startup mode
```

## Checking the effect after a deploy

- Request rate from the app to Meilisearch: about one document request per
  index per flush interval while there is traffic, plus one `GET /tasks` per
  check interval. `GET /tasks/<uid>` polling should be gone apart from index
  setup, the backfill and discovery rebuilds.
- Disk writes of the Meilisearch container (`docker stats`, `iotop`,
  `pidstat -d 5`) should drop well below the previous 2.87 MB/s.
- Post something with a unique word; it should show up in
  `GET /api/v2/search?q=<word>&type=statuses` within about 5-10 s.
- Delete it; it disappears from results right away (results are re-read from
  the database) and from the index within about one flush interval.
- `grep "search index"` in the app log: no repeated `batch failed` /
  `giving up` / `queue full` warnings.

The discovery index is a separate periodic writer: in `auto` mode it builds a
new generation of up to 5000 recent public posts about every 2-3 minutes (an
index older than 5 minutes is treated as expired) and deletes the old one.
Its task polling now also backs off (50 ms doubling to 1 s). If Meilisearch
writes are still high, compare with recommendations in `rules` mode.
