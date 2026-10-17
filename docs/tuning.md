# Tuning (stub)

This page collects tuning advice for medium and larger instances. It is a starting point, not a benchmark-backed guide. Always measure on your own hardware.

## What uses memory

Measured on the demo instance (c.hime.at, October 2026, a few users, federating normally):

| Process | Resident memory |
| --- | --- |
| Community (single Go process) | ~82 MiB, ~2% CPU |
| Redis | ~141 MiB |
| Meilisearch (optional) | ~932 MiB |
| Docker image size | 396 MB |

Meilisearch is by far the largest component. Without it, search falls back to the database.

## PostgreSQL on slow disks (HDD / cheap VPS storage)

Federation causes many small writes. On spinning disks or throttled cloud volumes, these settings usually help (adjust to your RAM):

```conf
shared_buffers = 256MB            # ~25% of RAM dedicated to PostgreSQL
effective_cache_size = 768MB      # ~50-75% of RAM
commit_delay = 1000               # microseconds; groups concurrent commits into one fsync
commit_siblings = 5
wal_compression = on
max_wal_size = 2GB                # fewer forced checkpoints
checkpoint_timeout = 15min
checkpoint_completion_target = 0.9
# synchronous_commit = off        # faster, but the last few transactions can be lost on a crash
```

## Built-in write batching

Community already batches the heavy federation writes: outgoing delivery jobs are inserted with `CreateInBatches(…, 100)` (`activitypub/deliver.go`, `activitypub/outbound.go`) inside the same transaction as the post, instead of one row per remote inbox.

## Timelines and workers

See [feed-runtime.md](feed-runtime.md) (Feed workers, Redis, `FEED_MODE`) and [timeline-performance.md](timeline-performance.md).

## TODO

- Real measurements on a 1 vCPU / 1 GB VPS (SQLite, no Meilisearch).
- MySQL equivalents of the settings above.
