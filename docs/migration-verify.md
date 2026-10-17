# Verifying a Misskey import (rehearsal)

`scripts/migration/verify_misskey_import.py` checks an isolated Community
instance after a Misskey import. It is **read-only**: SQL sessions are opened
read-only and only `SELECT` is issued; HTTP uses only `GET`/`OPTIONS` against
`COMMUNITY_BASE` and never follows redirects. Keys are compared by SHA-256 of
the normalized public PEM; private keys, tokens and passwords are never read.

Requirements: Python 3 stdlib; `psycopg2` if installed, else `psql`; `mysql`
CLI if the Community DB is MySQL (`mysql://user:pass@host:port/db`).

```sh
MISSKEY_DB_DSN='postgresql://misskey_ro@127.0.0.1:5432/misskey' \
COMMUNITY_DB_DSN='postgresql://community@127.0.0.1:5432/community_rehearsal' \
COMMUNITY_BASE='http://127.0.0.1:18080' HOST_HEADER=maid.lat SAMPLE=50 \
python3 scripts/migration/verify_misskey_import.py --seed 42
```

Prefer `PGPASSFILE`/`~/.pgpass` over passwords in the DSN.

| # | Check | Source of truth |
|---|-------|-----------------|
| 1 | Local user count | Misskey `"user" WHERE host IS NULL` (also without `isDeleted`) vs Community `accounts` with `domain IS NULL` and `local_actor_uri LIKE 'https://<host>/users/%'` |
| 2 | Sampled users: `GET /users/<id>` (AP Accept, `Host: <host>`) id = `https://<host>/users/<id>`, `publicKey.id` = `…#main-key`, PEM hash = Misskey `user_keypair."publicKey"`; WebFinger `acct:<username>@<host>` `rel=self` = that id | |
| 3 | Sampled local `public`/`home` notes: `GET /notes/<id>` id = `https://<host>/notes/<id>` | Misskey `note` |
| 4 | Per sampled user follower/following counts | Misskey `following` (`followeeId`/`followerId`, local+remote) vs Community `follows` (`follow_id`/`account_id`, `status = 0`) |
| 5 | Instance actor: `accounts.local_actor_uri = 'https://<host>/actor'` has an `account_keys` row, and `GET /actor` serves that key | |
| 6 | Local custom emoji (`emoji.host IS NULL` vs `emojis.domain = ''`) and drive files (`drive_file` vs `media_attachments`) within `--tolerance` (2%) | |
| 7 | `/users/<id>/inbox` is routed (POST-only, so `OPTIONS`/`GET` must not be 404) | |

System actors (usernames with a dot, e.g. `instance.actor`, `relay.actor`) are
excluded from the samples. Output is one `PASS`/`FAIL` line per check plus a
summary. Exit codes: `0` all pass, `1` a check failed, `2` usage/connection error.
Notes: imported remote follow targets that could not be resolved, or follows still
pending (`status = 1`), show up as count mismatches in check 4 and should be
reviewed rather than auto-fixed. `X-Forwarded-Proto: https` is sent by default
(`--forwarded-proto ''` to disable).
