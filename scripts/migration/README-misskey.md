# Misskey administrator export

`export_misskey.py` creates a **private site migration bundle**, not a public
personal backup. It contains password hashes, AP private keys, email addresses,
MFA material and complete raw source rows. Keep the directory private and encrypt
it for transport/storage. Never upload it to a personal-import endpoint.

Requirements: Python 3.9+ on Linux/POSIX, PostgreSQL `psql`, a read-only connection
to a consistent Misskey PostgreSQL database (or a restored database snapshot),
and a trusted snapshot of Misskey's `files` directory. No third-party Python
packages are required. The adapter was checked against Misskey **2026.10.0**.

Set `PGHOST`, `PGPORT`, `PGDATABASE`, `PGUSER` and a private `PGPASSFILE` using your
normal secret-management workflow. Use `PGSSLMODE=verify-full` and `PGSSLROOTCERT`
for remote database connections. The script accepts no password/DSN command-line
argument, does not print PostgreSQL stderr, and disables `.psqlrc` and password
prompts. Do not enable shell tracing around credentials.

```sh
python3 scripts/migration/export_misskey.py \
  --base-url https://your-misskey.example \
  --source-version 2026.10.0 \
  --id-method aidx \
  --source-dir /private/consistent-snapshot/files \
  --output /private/new-migration-bundle
```

The output directory must not exist. It is created with mode 0700; bundle and
media files are 0600. `bundle.json` uses `community-transfer/v1` and `scope=site`.
The command reads all public base tables within one **READ ONLY REPEATABLE READ**
transaction, captures its snapshot ID, and retains every row in private Records.
The database query has statement/lock timeouts. It does not alter source rows,
change domains, restart services, deliver AP activities or perform a cutover.

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
- `1`: export failed; there may be private partial assets but no complete migration
  is claimed. Existing output paths are not overwritten or automatically removed.

The default source-row limit is 1,000,000, JSON byte budget 512 MiB and media budget
8 GiB. They are configurable through `--max-records`, `--max-snapshot-bytes` and
`--max-media-bytes`. Rows are held in memory for conversion, so memory usage can
exceed encoded JSON size. Use conservative limits on small machines. Large-site
streaming conversion requires further work; do not assume unbounded scale.
Database consistency alone does not freeze a live object store. Use an offline or
coordinated consistent media snapshot and a separately planned final cutover.

Run source/security tests:

```sh
python3 -m unittest discover -s scripts/migration -p 'test_export_misskey.py' -v
```
