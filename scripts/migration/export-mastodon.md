# Mastodon administrator export

Run `export_mastodon.rb` with `rails runner` in the **original Mastodon 4.7.3 environment**, with its existing database, storage and ActiveRecord encryption configuration. This is an administrator site export; it is not a personal archive importer.

```sh
TRANSFER_OUTPUT=/private/new-site-export \
  RAILS_ENV=production bundle exec rails runner /trusted/export_mastodon.rb
```

The output parent must already exist with no symlink path components; the destination must not exist. The script creates a directory with mode `0700`, files with `0600`, and writes `bundle.json` plus checksum-addressed media paths. It preserves source bigint IDs as JSON strings and original AP actor/object/key URIs. It never prints passwords, private keys, raw rows or connection strings. Treat the entire output as a secret: it includes decrypted signing keys, password verifiers, optional decrypted TOTP and complete private source records. Transfer it through an authenticated encrypted channel; never put it in public storage or a source checkout.

All PostgreSQL reads run in a `REPEATABLE READ`, `READ ONLY` transaction, with the transaction properties checked before export. No ORM create/update hooks run. Media bytes are external to the PostgreSQL snapshot: freeze source file changes for an authoritative cutover snapshot. The script copies originals and explicit thumbnails through the source Paperclip adapter and checks their declared length and SHA256. It does not fetch absent originals from arbitrary remote URLs. Missing files produce errors when the source declares stored original bytes. Native uncached remote attachments retain a validated credential-free HTTPS reference with a warning; they have no fabricated path, bytes or checksum. Original type/metadata remain in private Records.

Exit codes:

- `0`: bundle completed without export errors; destination preflight still required.
- `2`: bundle completed with explicit blocking issues, such as missing media, unsupported active keys, limited visibility, 2FA or configured password pepper.
- `1`: export failed; any private partial output remains for operator inspection. Do not import a partial bundle.

The canonical sections implement `datatransfer/types.go`. Complete raw database rows are also retained in `records`, including data with no live Community equivalent; preservation warnings are not claims of operational conversion. Administrative roles, sessions, unread state, historical versions, special actor types and other policies must pass destination capability checks. Only the official instance actor is marked `system`; arbitrary local accounts lacking a User are errors. The canonical administrator flag is taken from Mastodon’s explicit administrator privilege; other role policy remains in Records. Only trusted site imports may apply it; user credentials and personal imports cannot confer administrator authority.

Do not replay the exported historical posts/follows as new federation traffic. Same-domain migration must preserve old routes and keys at the destination. Run target dry-run/preflight and verify audience/credentials/files before changing routing. Ordinary user backups cannot authorize this site-level identity takeover.

Standalone format and safety tests, using Ruby and minitest:

```sh
ruby scripts/migration/export_mastodon_test.rb
```

Tested on the original 4.7.3 Rails environment with a real read-only PostgreSQL snapshot: canonical identities/keys, exact media bytes and private output modes. Fixture-specific findings belong in private validation reports, not in this reusable script.

The Rails exporter prints `progress stage=<section> done=N speed=R/s` on stderr every `TRANSFER_PROGRESS_INTERVAL` seconds (default 5). On failure it prints the section, exception class and message (passwords and URL credentials redacted); `TRANSFER_VERBOSE=1` adds the backtrace.

## Direct database export (`export_mastodon_db.py`)

When the original Rails environment is unavailable (this is what the setup wizard uses), `export_mastodon_db.py` reads the Mastodon PostgreSQL database directly with the same resumable design as `export_misskey.py` (see `README-misskey.md`): keyset-paged reads in short `READ ONLY` transactions with `default_transaction_read_only=on`, a SQLite state directory (`<output>.state` or `--state-dir`) checkpointed per page, a streamed `bundle.json`, and bounded memory.

By default only local data is exported: local accounts and everything they own, plus the remote accounts, statuses (reply/boost targets, favourited/bookmarked/pinned statuses, remote replies to local statuses), mentions, tags, media, polls, keys and emojis those reference. `--include-remote-cache` exports every table completely. `preview_cards` and `conversations` are not read in local-only mode.

```sh
python3 scripts/migration/export_mastodon_db.py --base-url https://masto.example \
  --media-dir /srv/mastodon/public/system --output /private/mastodon-bundle          # one pass (source stopped)
# or two phases on a live source:
python3 scripts/migration/export_mastodon_db.py ... --fetch-only --max-rows-per-sec 2000
python3 scripts/migration/export_mastodon_db.py ... --final-sync                     # after writes are stopped
```

Common options: `--dsn` (no password; use `PGPASSFILE`), `--batch-size`, `--max-rows-per-sec`, `--batch-sleep`, `--progress-interval`, `--verbose`, `--list-object-keys`. Errors print the stage and cause; exit codes are 0 / 2 (blocked) / 1 (failed, rerun resumes).
