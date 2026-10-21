# Verifying a Misskey import (rehearsal)

`scripts/migration/verify_misskey_import.py` checks an isolated Community
instance after a Misskey import. It is **read-only**: SQL sessions are opened
read-only and only `SELECT` is issued; HTTP uses only `GET`/`OPTIONS` against
`COMMUNITY_BASE` and never follows redirects. Keys are compared by SHA-256 of
the normalized public PEM; private keys, tokens and passwords are never read.

The import being verified is produced in two steps (there is no single
`tootctl` command for a site import): export the source with
`scripts/migration/export_misskey.py`, then preflight and apply the bundle with
`community-transfer --scope site --input <bundle>` (or let the setup wizard run
both). The export is local-only by default: remote accounts and notes are
included only when local data references them, so remote-side counts on the
target are expected to be far below Misskey's remote cache. Checks 1–7 only
compare local data and follow relations of local users, which the default
export includes; use `--include-remote-cache` only if the rehearsal must also
carry the full remote cache. See
[scripts/migration/README-misskey.md](../scripts/migration/README-misskey.md).

Requirements: Python 3 stdlib; `export_misskey.py` next to the script (the value rules are imported from it); `psycopg2` if installed, else `psql`; `mysql`
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
| 0 | The target is populated: Community has local accounts and, when Misskey has live local users, at least one `https://<host>/users/*` identity (an empty target database fails instead of passing vacuously) | |
| 1 | Local user count | Misskey `"user" WHERE host IS NULL` (also without `isDeleted`) vs Community `accounts` with `domain IS NULL` and `local_actor_uri LIKE 'https://<host>/users/%'` |
| 1b | Every live local Misskey user (not only the count) has a Community account with `local_actor_uri = https://<host>/users/<id>`; missing users are listed | Misskey `"user"` |
| 2 | Sampled users: `GET /users/<id>` (AP Accept, `Host: <host>`) id = `https://<host>/users/<id>`, `publicKey.id` = `…#main-key`, PEM hash = Misskey `user_keypair."publicKey"`; WebFinger `acct:<username>@<host>` `rel=self` = that id | |
| 2-full | **Every** live local Misskey user, including the `instance.actor` and `relay.actor` system actors, at the database level (no HTTP, no sampling): exactly one Community account with `accounts.local_actor_uri = https://<host>/users/<misskey id>`, one `account_keys` row whose `public_key` SHA-256 (whitespace-normalized PEM) equals Misskey `user_keypair."publicKey"`, and `key_id` = `…#main-key` when set. Every mismatch is listed (missing account, missing Misskey or Community key row, different key hash, wrong keyId, duplicate accounts); any mismatch is a FAIL. Community `/users/*` accounts with no live Misskey user (e.g. deleted on the source) are `[INFO]`. **Blocks the DNS cut.** | Misskey `"user"` + `user_keypair` vs Community `accounts` + `account_keys` |
| 3 | Sampled local `public`/`home` notes: `GET /notes/<id>` id = `https://<host>/notes/<id>` | Misskey `note` |
| 4 | Per sampled user follower/following counts | Misskey `following` (`followeeId`/`followerId`, local+remote) vs Community `follows` (`follow_id`/`account_id`, `status = 0`) |
| 5 | Instance actor: the original URI is preserved. The Community account with `accounts.local_actor_uri = 'https://<host>/users/<instance.actor id>'` (Misskey's own URI, **not** `/actor`) has an `account_keys` row with keyId `…#main-key` whose key equals the public key of Misskey's `instance.actor` user (`user_keypair`); `system_settings` `federation.instance_actor_uri` designates that URI; and both `GET /users/<id>` and `GET /actor` serve that id, keyId and key | Misskey `user_keypair` |
| 5b | Relay actor: same as 5 for Misskey's `relay.actor` (`federation.relay_actor_uri`, `GET /users/<id>`); WARN when Misskey has no `relay.actor` | Misskey `user_keypair` |
| 5c | Relay subscriptions: every Misskey `relay` row is a Community `relays` row followed by the relay actor's original URI | Misskey `relay` |
| 6 | Local custom emoji (`emoji.host IS NULL` vs `emojis.domain = ''`) and drive files (`drive_file` vs `media_attachments`) within `--tolerance` (2%) | |
| 7 | `/users/<id>/inbox` is routed (POST-only, so `OPTIONS`/`GET` must not be 404) | |
| 8a–8g | Misskey profile extensions per account (is_cat, language, location, birthday, followed_message, moved_to, also_known_as), local + imported remote; expected values follow the importer's value rules (values dropped by rule are `[INFO]`, not expected); 8d / 8g FAIL when the source has set values but none parsed | [field mapping](misskey-field-mapping.md#value-rules) |
| 8h–8m | Account privacy settings; 8m `lastActiveDate` newer on the source than the import is `[INFO]` (active after extraction), bounded by `--extracted-at` (env `EXTRACTED_AT`, the bundle's `source.exported_at`) when given; defaults to `source.exported_at` of `--bundle` (env `BUNDLE`: bundle.json/dir, import `--report` JSON or direct-mode work dir) when that is set | |
| 9 | Pinned notes of local users (count); remote users' pins are `[INFO]` (not imported by design) | Misskey `user_note_pining` |
| 10a | Avatar decoration definitions: every Misskey `avatar_decoration` row was imported (a `data_transfer_receipts` row of kind `avatar_decoration` pointing at an existing Community `avatar_decorations` row); unimported ids are listed (the import report's `avatar_decoration_image_unavailable` says why). WARN when Misskey has no `avatar_decoration` table | Misskey `avatar_decoration` |
| 10b | Avatar decoration selections, per live local account: the Community `accounts.avatar_decorations` list equals Misskey `user."avatarDecorations"` with each id mapped to its imported decoration and the geometry (`angle`, `flipH`, `offsetX`, `offsetY`) compared after the importer's clamping/rounding; accounts with a difference, a missing selection, or a Community selection Misskey does not have FAIL. Selected ids that are not Misskey definitions are skipped by the importer and `[INFO]` here | Misskey `user."avatarDecorations"` |

System actors (usernames with a dot, e.g. `instance.actor`, `relay.actor`) are
excluded from the samples (checks 2, 4, 7) but included in 2-full.

Check 2 is an HTTP sample (`SAMPLE`, default 50); 2-full covers every local
user in the database. Before the DNS cut run both, and run check 2 once over
all users with `SAMPLE` set to at least the local user count (e.g.
`SAMPLE=100000`) so every actor document is also fetched over HTTP.

Avatar decorations: imported decorations are enabled for everyone (Misskey
role limits are dropped because roles are not migrated); descriptions are not
stored (Community decorations have none); the image is imported as an
ownerless site media file like a custom emoji image. Output is one `PASS`/`FAIL` line per check plus a
summary; `[INFO]` lines are informational and never change the exit code. Exit codes: `0` all pass, `1` a check failed, `2` usage/connection error.
Connection failures are reported as `no response (<error>)` rather than a fake
`HTTP 0`; count checks fail when the source has rows and the target has none
(the 2% tolerance never makes `0` match a non-zero count).
Notes: imported remote follow targets that could not be resolved, or follows still
pending (`status = 1`), show up as count mismatches in check 4 and should be
reviewed rather than auto-fixed. `X-Forwarded-Proto: https` is sent by default
(`--forwarded-proto ''` to disable).
