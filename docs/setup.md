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

1. Start the container with an empty `./config` directory.
2. Read the one-time token from the log:
   `docker compose logs community | grep -i "setup token"`.
3. Open `https://<your domain>/setup` (any path redirects there).
4. Steps: language, token, install type, (Misskey source), domain, database
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
* Stages: write configuration, schema, instance settings
  (`tootctl admin import-misskey-meta`), media, export
  (`scripts/migration/export_misskey.py`), preflight, import, passkeys
  (`tootctl passkeys import-misskey`), administrator, finish.
* Existing Misskey users keep their passwords. Naming an existing user as the
  administrator makes them the owner.

## Configuration file

* Path: `ENV_FILE`, default `/app/config/.env` (mount `./config:/app/config`).
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

The command copies every setting the admin UI can edit into the database and
prints the `.env` lines that can then be removed. Values left in `.env` keep
overriding the database. Restart after editing `.env`.

## docker-compose changes

* New volumes: `./config:/app/config` (configuration and marker) and
  `./data:/app/data` (SQLite, setup work files).
* The host `.env` is optional (`env_file` with `required: false`, Compose
  v2.24 or later). `APP_BASE_URL`, `ENCRYPTED_KEY` and `DB_PASSWORD` are no
  longer required by Compose, so an unconfigured container can start the
  wizard.
* `DB_HOST`/`REDIS_0_*` are no longer forced. `COMMUNITY_DEFAULT_DB_HOST=db`
  and `COMMUNITY_DEFAULT_REDIS_HOST=redis` are used only when the
  configuration names no host.
* With a bundled database (`COMPOSE_PROFILES=postgres|mysql`), keep
  `COMPOSE_PROFILES`, `DB_DATABASE`, `DB_USERNAME` and `DB_PASSWORD` in the
  host `.env` for the database container and enter the same values in the
  wizard (host `db`).
* The image includes `python3` and `psql` for the Misskey exporter.

## Upgrading an existing deployment (ops)

The host `.env` keeps working as before, so the upgrade itself needs no
configuration change. The steps below move the configuration into the
mounted directory.

1. Back up the database and the current `.env`.
2. Pull the new version and its `docker-compose.yml`, then
   `mkdir -p config data`.
3. `docker compose up -d`. On first boot the server sees the existing
   database, writes `config/.installed` and the `system.installed_at` row, and
   starts normally. Check: `ls config/.installed` and
   `curl -s -o /dev/null -w '%{http_code}' https://<domain>/setup` → `404`.
4. Move the configuration:
   ```sh
   cp .env config/.env && chmod 600 config/.env
   ```
   Reduce the host `.env` to what Compose itself needs (`COMPOSE_PROFILES`,
   `DB_DATABASE`/`DB_USERNAME`/`DB_PASSWORD` for a bundled database, any
   `MEILISEARCH_*` used for interpolation), or delete it if nothing is needed.
   Then `docker compose up -d` again. Fields from `config/.env` now show
   "Locked by .env".
5. Optional: `tootctl settings import-env --dry-run`, then without
   `--dry-run`, and remove the printed lines from `config/.env`.

If `config/.env` is missing or unreadable after step 4, the server shows the
maintenance page instead of the wizard, because the installed marker exists.
