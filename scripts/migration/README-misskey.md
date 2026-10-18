# Misskey administrator export

> **Recommended:** run the migration in direct mode, `community-transfer --source misskey --source-dsn …`, which drives this exporter with `--emit ndjson` and imports the stream in chunks without a `bundle.json`. See [docs/migration.md](../../docs/migration.md). The bundle workflow below remains supported.

`export_misskey.py` creates a **private site migration bundle**, not a public
personal backup. It contains password hashes, AP private keys, email addresses,
MFA material and complete raw source rows. Keep the directory private and encrypt
it for transport/storage. Never upload it to a personal-import endpoint.

Requirements: Python 3.9+ on Linux/POSIX, PostgreSQL `psql`, a read-only
connection to the Misskey PostgreSQL database (live, a replica, or a restored
snapshot), and a trusted snapshot of Misskey's `files` directory. No third-party
Python packages are required. The adapter was checked against Misskey **2026.10.0**.

## What is exported

By default the export is **local-only**: local users with their profiles, keys,
notes, files, follows, blocks, mutes, lists, bookmarks, pins, polls, chats and
other rows they own, plus exactly the remote entities those reference:

- remote accounts the local users follow / are followed by / block / mute /
  list / chat with, authors of referenced notes, users who reacted to or voted
  on local notes (with their profiles, public keys and avatar/banner files);
- remote notes that local notes reply to, quote or renote, notes local users
  reacted to, bookmarked, pinned, clipped or voted on, and remote replies and
  quotes of local notes (pure remote renotes are skipped);
- reactions on local notes, files attached to any exported note, and the
  emojis/instance rows of every exported remote host.

The rest of the remote-content cache (on a typical instance the vast majority
of `note`, `note_reaction`, `drive_file`, `user` rows) and derived chart tables
(`__chart*`) are not read. References that point outside the selection (for
example the parent of a remote reply) are dropped from the canonical fields
with an `info` issue `remote_reference_trimmed`; the raw row in Records keeps
the original value. Add `--include-remote-cache` to export every row of every
table instead (much larger; needs disk for the state directory and bundle).

## Connection and read-only access

Set `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER` and a private `PGPASSFILE`
(mode 0600) or pass `--dsn 'host=... port=... dbname=... user=... sslmode=verify-full'`.
A DSN containing a password is refused; passwords only come from `PGPASSFILE`.
Every query runs with `default_transaction_read_only=on` inside an explicit
`READ ONLY` transaction, with statement/lock timeouts. A dedicated role makes
read-only access explicit on the server side too:

```sql
CREATE ROLE community_export LOGIN PASSWORD '...' NOSUPERUSER NOCREATEDB NOCREATEROLE;
ALTER ROLE community_export SET default_transaction_read_only = on;
GRANT CONNECT ON DATABASE misskey TO community_export;
GRANT USAGE ON SCHEMA public TO community_export;
GRANT SELECT ON ALL TABLES IN SCHEMA public TO community_export;
```

For a remote source, either use TLS (`sslmode=verify-full`, `PGSSLROOTCERT`) or
an SSH tunnel and connect to the local end:

```sh
ssh -N -L 15432:127.0.0.1:5432 admin@misskey-db.example &
python3 scripts/migration/export_misskey.py --dsn 'host=127.0.0.1 port=15432 dbname=misskey user=community_export' ...
```

## Running the export

```sh
python3 scripts/migration/export_misskey.py \
  --base-url https://your-misskey.example \
  --source-version 2026.10.0 \
  --id-method aidx \
  --source-dir /private/consistent-snapshot/files \
  --output /private/new-migration-bundle
```

The run has two steps sharing a **state directory** (default `<output>.state`,
or `--state-dir`):

1. **fetch** — every needed table is read with keyset pagination
   (`WHERE pk > last ORDER BY pk LIMIT --batch-size`, default 5000), one short
   read-only transaction per page, and upserted into a local SQLite file. The
   last key of every table is checkpointed after each page, so an interrupted
   run (Ctrl-C, lost connection, OOM-killed host) resumes from the checkpoint
   when the same command is run again. No long snapshot is held, so autovacuum
   on the source is not blocked.
2. **build** — the staged rows are streamed into `bundle.json` (each section is
   spooled to disk and concatenated), and media bytes are copied.

Memory stays bounded (SQLite page cache capped at 64 MiB; only small local
lookup sets are kept in memory). Measured on a synthetic Misskey database:

| dataset | mode | peak RSS (python + psql) | time |
| --- | --- | --- | --- |
| 200k remote + 2k local notes | default (local-only) | 47 MiB | 2.5 s |
| 200k remote + 2k local notes | `--include-remote-cache` | 116 MiB | 19 s |
| 1M remote + 10k local notes | default (local-only) | 59 MiB | 4 s |
| 1M remote + 10k local notes | `--include-remote-cache` | 117 MiB | 95 s (3.2 GB bundle) |

Disk: the state directory is roughly the size of the selected rows as JSON,
and the bundle again about the same.

### Two-phase migration (live source)

A consistent result without stopping the source for the whole export:

```sh
# phase 1 – source live: bulk pass (rate limited for a busy/spinning-disk server)
python3 scripts/migration/export_misskey.py ... --fetch-only --max-rows-per-sec 2000
# put Misskey into maintenance / stop all writers (web, workers, queue)
# phase 2 – final catch-up and build
python3 scripts/migration/export_misskey.py ... --final-sync
```

`--final-sync` continues every table from its checkpoint (new rows), re-reads
rows whose `updatedAt` is newer than the start of the bulk pass (minus a 5 minute
margin), checks every staged primary key against the source and drops rows that
were deleted, resolves references of the new rows, and then builds the bundle.
Tables without `updatedAt` only receive new rows and deletions in the final pass,
so the final pass must run after writes are stopped. Rows of tables without a
single-column primary key are re-read whole on every run.

Throttling: `--max-rows-per-sec N` caps the average read rate, `--batch-sleep S`
sleeps after every page, `--batch-size` sets the page size.

### Progress and errors

Every few seconds (`--progress-interval`, default 5) a line such as

```
progress stage=notes done=4210/9536 (44.1%) eta=12s speed=430/s
```

is printed on stderr for each stage (accounts, notes, follows, reactions, keys,
files, `…/referenced`, `…/deletions`, then `build/…`). After a resume the
counter continues from the checkpoint. On failure the exporter prints the stage,
the exception type and its message (psql's own error and warnings, such as a
`PGPASSFILE` permission warning, are passed through with passwords redacted)
and exits 1; `--verbose` adds the traceback. Fetched data stays in the state
directory for the next attempt. stdout carries only the final JSON summary.

The output directory must not exist. It is created with mode 0700; bundle and
media files are 0600. `bundle.json` uses `community-transfer/v1` and `scope=site`
and is imported with `community-transfer` (see `docs/data-migration.md`).
The exporter does not alter source rows, change domains, restart services,
deliver AP activities or perform a cutover.

Supply the actual source `config.id`: `aid`, `aidx`, `meid`, `meidg`, `objectid`
and `ulid` are supported. IDs remain strings; creation timestamps are decoded
using the selected official layout. The original website origin is mandatory;
localhost is never guessed. Local actors remain `/users/{source-id}`, keys retain
`#main-key`, and local Notes remain `/notes/{source-id}`. A target must preserve
these paths and the original mathematical keys to maintain identity.

Local credentials use canonical scheme `bcrypt` (raw bcrypt, **no Community
prehash/salt**). Accounts with no password remain disabled for password login.
System accounts are identified through the source `system_account` table and
remain disabled for interactive login. Raw MFA/security-key/session records are
preserved; their presence does not authorize silently disabling MFA or importing
sessions. Target-side credential/role/identity validation is still required.

Media files are opened relative to the trusted source root with no symlink
traversal, copied byte-for-byte, checked against source size, checked for changes
during copying, and named by SHA-256. Originals and declared thumbnail/webpublic
variants are kept separately. Misskey may reserve derivative keys without
creating a derivative; a derivative URL declares that bytes must exist.
Object-storage files and missing local bytes produce **errors**, never a
successful metadata-only backup. Remote `isLink` files stay explicit references
with warnings; this exporter does not fetch arbitrary URLs. Local custom emoji
URLs can resolve to the same local `/files/{key}` snapshot; other storage layouts
need an adapter and otherwise produce errors.

Private Note recipients come from `visibleUserIds`, never textual mentions.
Note `ConversationID` is `note-thread:{threadId ?? id}`. This follows the official
2026.10.0 `NoteCreateService`: replies inherit `reply.threadId ?? reply.id`,
while root Notes store a null threadId. The target combines this thread identity
with the exact sender/recipient set, so same-audience replies share a conversation
and narrower branches remain separate. Using each reply's own ID splits history
into one conversation per message and is not an equivalent migration.
One-to-one old/new chats map to direct posts with stable pair conversation IDs.
For 2026.10 room chat, the official timeline checks current room membership or
ownership and then reads all room history, so the snapshot owner/member set is
retained as recipients. Moderator access remains a separate management concern,
not a mass addition of moderators to all private messages. Dynamic role policies
that can disable chat access are not approximated: affected new chat history is
kept archive-only with an issue. Old group chats with unproven ACLs also stay
archive-only. Read states, approvals, invitations and chat reactions are
preserved in Records; the target needs explicit adapters for equivalent behavior.
No new chat federation is authorized by this export.

Quote Notes and channels cannot currently be represented exactly by
the canonical basic fields. Such Notes remain archive-only. Timed mutes retain
expires_at, including expired entries; they must not become permanent mutes. Source roles, settings,
MFM, polls, reaction aggregates and all other unmodeled data remain available in
Records. A warning is not a claim that an unsupported feature now works.

Exit status:

- `0`: bundle written; explicit warnings still require target preflight/review.
- `2`: bundle written but **blocked** by asset/data errors; do not execute migration.
- `1`: export failed (the stage and cause are printed); rerun to resume. Existing
  output paths are not overwritten or automatically removed.
- `130`: interrupted; rerun the same command to resume.

`--max-media-bytes` (default 8 GiB) caps copied media; `--max-records`
optionally caps the number of staged rows. `--max-snapshot-bytes` is accepted
for compatibility and ignored.
Database consistency alone does not freeze a live object store. Use an offline or
coordinated consistent media snapshot and a separately planned final cutover.

Run source/security tests (the PostgreSQL integration tests are opt-in and drop
the public schema of the named scratch database):

```sh
python3 -m unittest discover -s scripts/migration -p 'test_export_*.py' -v
EXPORT_TEST_PG_DB=scratch python3 -m unittest discover -s scripts/migration -p 'test_export_pg_integration.py' -v
```

`scripts/migration/testdata/synthetic_misskey.sql` builds a Misskey-shaped
database of any size (`-v remote_notes=200000 -v local_notes=2000`) for
selection and memory checks.
