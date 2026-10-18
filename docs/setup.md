# Setup wizard, configuration file and settings

## How the server decides what to start

| State | Result |
| --- | --- |
| No database configuration **and** no `.installed` marker | **Setup mode**: only `/setup` and `/api/setup/*` are served |
| Database configured and reachable | Normal start; the installed marker is written or backfilled |
| Database configured but unreachable | **Maintenance** (HTTP 503); the server retries every 15 s and resumes by itself |
| `.env` unreadable, or missing while the `.installed` marker exists | **Maintenance**; never setup |

"Installed" means any of: the `.installed` file in the config directory, the
`system.installed_at` row in `system_settings`, or application tables in the
database. A server that is already running gets both markers written on its
first boot with this version, so it can never fall back into setup mode. After
install every `/setup` and `/api/setup/*` URL returns 404, enforced by the
server.

## Running the wizard

1. For a fresh Compose installation, copy `.env.setup.example` to `.env`,
   select one database profile and set `BUNDLED_DB_PASSWORD`. Leave `DB_*`
   unset so the wizard opens (see [Compose configuration](#docker-compose-changes)).
   Start the container with an empty `./var/config` directory (mounted as
   `/app/config`). The repository's `config/` directory is Go source and is
   never used for runtime files.
2. Read the one-time token from the log:
   `docker compose logs community | grep -i "setup token"`.
3. Open `https://<your domain>/setup` (any path redirects there).
4. Steps: language, token, install type, (Misskey or Mastodon source and media), domain, database
   (with a connection test; the target must be empty), Redis (with a test),
   first administrator, review.
5. The install runs as a background job. Progress, counts and a log are saved,
   so you can close the page; a failed stage can be retried and finished stages
   are skipped. When it finishes the server restarts itself and the page goes
   to the sign-in screen.

The chosen language becomes the instance default and the administrator's
interface language. The domain cannot be changed later.

### Misskey migration

* Stop Misskey (or point the wizard at a copy of its PostgreSQL database). The
  source is only read.
* The source connection is checked before the media step. If the ID format
  cannot be detected, select the `id` value from the original `default.yml`.
* The domain is read from the source and shown read-only. Remote servers keep
  seeing the same actor and object URIs, RSA keys and followers; old
  `/users/<id>` and `/notes/<id>` URLs keep resolving. Point DNS at the new
  server after the job finishes.
* Media from a local directory: mount Misskey's `files` directory into the
  container (e.g. `- /srv/misskey/files:/misskey/files:ro`) and enter that path.
* Media from object storage (S3, R2, MinIO): the settings are prefilled from
  Misskey's meta. **Reference in place** keeps the original URLs and copies
  nothing (the bucket must stay online). **Copy** downloads every object into
  `SETUP_WORK_DIR` (resumable, with file and byte progress).
  References only require the public media URL; copy mode shows credentials
  and tests bucket access before continuing. Hidden storage credentials are
  omitted when submitting a local-directory or reference migration.
* Stages: write configuration, schema, instance settings
  (`tootctl admin import-misskey-meta`), media, export
  (`scripts/migration/export_misskey.py`), preflight, import, passkeys
  (`tootctl passkeys import-misskey`), administrator, finish.
* Existing Misskey users keep their passwords. Naming an existing user as the
  administrator makes them the owner.

### Mastodon migration

* Stop Mastodon (web, streaming and Sidekiq), or point the wizard at a copy of
  its PostgreSQL database. The source is only read: the wizard connects
  directly and runs `scripts/migration/export_mastodon_db.py` in one
  read-only snapshot. The Rails environment is not needed.
* The domain is detected from local status URIs (`LOCAL_DOMAIN`) and shown
  read-only. Actor URIs (`/users/<name>`), status URIs
  (`/users/<name>/statuses/<id>`), the RSA keys (the `keypairs` table, or
  `accounts.private_key` on older versions) and remote followers are kept, so
  remote servers see the same identity. Followers-only and direct posts stay
  private. Point DNS at the new server after the job finishes.
* Media from a local directory: mount Mastodon's `public/system` directory
  into the container (e.g. `- /srv/mastodon/public/system:/mastodon/public/system:ro`)
  and enter that path. Remote media is read from its `cache/` prefix when
  present; otherwise the remote URL is kept.
* Media from object storage (S3, R2, MinIO): Mastodon keeps these settings in
  `.env.production`, not in its database. **Reference in place** only needs
  the public media URL (`S3_ALIAS_HOST`, or the bucket URL); it keeps
  `<public URL>/<key>` URLs and copies nothing. **Copy** requires
  `S3_ENDPOINT`, `S3_BUCKET`, `AWS_ACCESS_KEY_ID` and `AWS_SECRET_ACCESS_KEY`,
  with optional `S3_REGION`. It tests bucket access before continuing and
  downloads only the objects the database references into `SETUP_WORK_DIR`
  (resumable, with file and byte progress). Copy mode does not require the
  public media URL or a Misskey ID format.
* Stages: write configuration, schema, media, export, preflight, import,
  administrator, finish. Every stage records progress and counts, and a failed
  stage can be retried.
* Existing Mastodon users keep their passwords (bcrypt). Naming an existing
  user as the administrator makes them the owner. Mastodon users whose role
  has the Administrator permission (the `Owner` role by default) become
  administrators.

## Configuration file

* Path: `ENV_FILE`, default `/app/config/.env` (mount `./var/config:/app/config`).
  Outside a container the default is `.env` next to the binary.
* Written atomically with mode `0600`. `ENCRYPTED_KEY` and `MEDIA_PROXY_KEY`
  are generated.
* Precedence: process environment > `.env` file > values saved in the admin
  UI > built-in defaults.
* `.env`-only (read-only in the admin UI): database, Redis, domain
  (`APP_BASE_URL`), secrets, listen address. Everything else can be edited in
  **Admin → Settings**. A field set in `.env` shows "Locked by .env" with the
  variable name; one set by the container environment shows "Locked by
  environment".
* SMTP changes apply immediately. Storage and search changes are marked
  "restart required". SMTP and storage have **Test** buttons. Every settings
  change is written to the admin audit log (secrets are recorded only as
  changed or cleared).

### Moving settings out of `.env`

```sh
docker compose exec community tootctl settings import-env --dry-run
docker compose exec community tootctl settings import-env
```

The command copies every setting the admin UI can edit into the database. It
then prints the variable names to remove, for example:

```
Saved. Now remove these variables:
  SMTP_PASSWORD
  SMTP_SERVER
Remove them from BOTH files, then recreate the container (docker compose up -d):
  - the host .env used by docker compose (env_file), and
  - /app/config/.env
```

Remove each listed name from **both** files:

* `var/config/.env` (`/app/config/.env` in the container), and
* the host `.env` next to `docker-compose.yml`. Compose loads it through
  `env_file`, so its entries become process environment variables. Those
  take precedence over both the file and the database. Deleting the line from
  `var/config/.env` alone changes nothing.

Then run `docker compose up -d` so the container is recreated. A plain
restart keeps the old environment.

The command shows a `WARNING: still set in this process environment` line for
any imported variable that is still in its own environment. After startup the
server also logs a warning that names every admin-editable setting stored in
the database but still set in the process environment.

## Database connection pool

Every process (the server, each `tootctl` run, `community-transfer`) opens
one main pool. HTTP handlers, the write buffer, the inbox workers and the
schedulers all share it. `DB_MAX_OPEN_CONNS` caps the connections of the
whole process: when the process also opens a pool to the same database
server, the main pool shrinks by that amount until it is closed. Examples are
the Misskey source database in `tootctl passkeys import-misskey` (2
connections) and the setup wizard's probes (1–2). SQLite always uses one
connection.

| Variable | Default | Meaning |
|---|---|---|
| `DB_MAX_OPEN_CONNS` | 30 | per-process cap on open connections |
| `DB_MAX_IDLE_CONNS` | 10 | idle connections kept (clamped to the cap) |
| `DB_CONN_MAX_LIFETIME` | 30m | Go duration or minutes; `DB_CONN_MAX_LIFETIME_MINUTES` still works |
| `DB_CONN_MAX_IDLE_TIME` | 10m | Go duration or minutes; `DB_CONN_MAX_IDLE_TIME_MINUTES` still works |

The effective values are logged at startup (`... connection pool: process
cap=30 (DB_MAX_OPEN_CONNS), main max_open=30, max_idle=10, aux reserved=0`).

**Sizing on a shared PostgreSQL.** Add the caps of every process that can run
at the same time and keep the sum well below the server's
`max_connections` minus `superuser_reserved_connections` minus what other
applications (e.g. Misskey) use. Example with `max_connections=100`: the server
at 30, plus one `tootctl`/`community-transfer` run at 30, leaves about 37 for
Misskey and admin sessions. Lower the cap of one-off tools with
`DB_MAX_OPEN_CONNS=10 tootctl ...` if Misskey needs more.

**Does 30 fit the workers?** Each inbox worker (`INBOX_WORKERS`, default 4)
uses at most one connection at a time. A job's transactions do not open a
second connection while they are held, and the test suite runs the inbox
queue on a 1-connection pool to prove it. The write buffer flushes in one
transaction on one connection. So 4 workers + 1 flush leave about 25
connections for HTTP requests and schedulers. When the pool is full, callers
wait for a free connection instead of deadlocking. If you raise
`INBOX_WORKERS`, keep `DB_MAX_OPEN_CONNS` at least `INBOX_WORKERS + 10`.

## docker-compose changes

* New volumes: `./var/config:/app/config` (configuration and marker) and
  `./data:/app/data` (SQLite, setup work files).
* The host `.env` is optional (`env_file` with `required: false`, Compose
  v2.24 or later). `APP_BASE_URL`, `ENCRYPTED_KEY` and `DB_PASSWORD` are no
  longer required by Compose, so an unconfigured container can start the
  wizard.
* `DB_HOST`/`REDIS_0_*` are no longer forced. `COMMUNITY_DEFAULT_DB_HOST=db`
  and `COMMUNITY_DEFAULT_REDIS_HOST=redis` are used only when the
  configuration names no host.
* For a **new installation with a bundled database**, copy
  `.env.setup.example` to `.env`, choose `COMPOSE_PROFILES=postgres` or
  `COMPOSE_PROFILES=mysql`, and set a strong `BUNDLED_DB_PASSWORD` before
  running `docker compose up -d --build`. Optional `BUNDLED_DB_DATABASE`
  and `BUNDLED_DB_USERNAME` configure the database name and user. Do not
  copy the manual `.env.example` or add `DB_*` variables for this flow:
  those indicate an already configured instance and skip the wizard.
* The wizard offers and preselects **Built-in database** only when Compose
  reports exactly one selected database profile and its connection settings
  are available. Set the profile in `.env` or the `COMPOSE_PROFILES`
  environment variable (not just the `--profile` command flag), so Compose
  passes this selection to the app. The password stays on the server;
  connection testing and installation use the same resolved configuration.
  Without a bundled database, only manual database options are shown.
* Legacy Compose deployments continue to use `DB_DATABASE`, `DB_USERNAME`
  and `DB_PASSWORD` when no `BUNDLED_DB_*` overrides are set. Keep the
  credentials of existing database volumes unchanged; editing these values
  does not rotate an initialized database's password.
* The image includes `python3` and `psql` for the Misskey and Mastodon exporters.

## Upgrading an existing deployment (ops)

The host `.env` keeps working as before, so the upgrade itself needs no
configuration change. The steps below move the configuration into the
mounted directory.

1. Back up the database and the current `.env`.
2. Pull the new version and its `docker-compose.yml`, then
   `mkdir -p var/config data`. If an earlier build of this
   compose file already wrote `config/.env`, `config/.installed` or
   `config/setup-*` into the source tree, move them:
   `mv config/.env config/.installed config/setup-* var/config/ 2>/dev/null`. A missing
   marker is rewritten on boot because the database already exists, so the
   server never enters setup mode.
3. `docker compose up -d`. On first boot the server sees the existing
   database, writes `var/config/.installed` and the `system.installed_at` row, and
   starts normally. Check: `ls var/config/.installed` and
   `curl -s -o /dev/null -w '%{http_code}' https://<domain>/setup` → `404`.
4. Move the configuration:
   ```sh
   cp .env var/config/.env && chmod 600 var/config/.env
   ```
   Reduce the host `.env` to what Compose itself needs (`COMPOSE_PROFILES`,
   `DB_DATABASE`/`DB_USERNAME`/`DB_PASSWORD` for a bundled database, any
   `MEILISEARCH_*` used for interpolation), or delete it if nothing is needed.
   Then `docker compose up -d` again. Fields from `var/config/.env` now show
   "Locked by .env".
5. Optional: `tootctl settings import-env --dry-run`, then without
   `--dry-run`. Remove the printed variables from both `var/config/.env` and the
   host `.env`, then run `docker compose up -d` again. Check the log for the
   "still set in the process environment" warning.

If `var/config/.env` is missing or unreadable after step 4, the server shows the
maintenance page instead of the wizard, because the installed marker exists.
