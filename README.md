<p align="center">
  <img src="frontend/public/logo.svg" alt="Community" width="88">
</p>

<h1 align="center">Community</h1>

<p align="center"><b>One small server for your corner of the fediverse: Mastodon apps, Misskey-style reactions and Matrix chat, all built in.</b></p>

<p align="center">
  <b>English</b> · <a href="README.zh-CN.md">简体中文</a> · <a href="README.zh-TW.md">繁體中文</a> · <a href="README.ja.md">日本語</a> · <a href="README.ru.md">Русский</a> · <a href="README.fr.md">Français</a> · <a href="README.de.md">Deutsch</a>
</p>

<p align="center">
  <img alt="Go" src="https://img.shields.io/github/go-mod/go-version/kasuganosora/community">
  <img alt="ActivityPub" src="https://img.shields.io/badge/ActivityPub-federated-8a2be2">
  <img alt="Matrix" src="https://img.shields.io/badge/Matrix-homeserver-0dbd8b">
  <a href="https://c.hime.at"><img alt="Demo" src="https://img.shields.io/badge/demo-c.hime.at-1e90ff"></a>
</p>

<p align="center">
  <img src="docs/images/desktop-home.jpg" alt="Community on desktop" width="72%">
  <img src="docs/images/mobile-about.jpg" alt="Community on mobile" width="22%">
</p>

Community is a self-hosted social server written in Go and Vue. You get timelines, follows, replies, polls, media and direct messages, and you can talk to people on Mastodon, Misskey and the rest of the fediverse. Chat rooms come from a Matrix homeserver that lives inside the same program, so there's nothing extra to run.

Try it on the live demo: **https://c.hime.at** (a test server, please don't use it for real).

## Why Community?

There are already great fediverse servers. Here's what Community does differently:

- **Use the Mastodon apps you already have.** Community implements the Mastodon client API, so you can log in with Mastodon apps (verified with Toot! on iOS) and keep your favourite workflow. On top of that it has Misskey-style things: emoji reactions (Unicode and custom), avatar decorations, and a full web UI. Reactions federate both ways with Misskey.
- **Chat is included.** A Matrix homeserver is built into the same server. Your users can join group chats from the web UI, or use Element and other Matrix clients, including end-to-end encrypted rooms, and talk to people on other Matrix servers. You don't run Synapse next to it.
- **Small and simple to run.** One Go binary serves the API, the web app, federation and Matrix. It works with PostgreSQL, MySQL or SQLite. Redis is required; Meilisearch for full-text search is optional. On our demo the main process uses about 82 MiB of memory.
- **Move your whole server over, keep your followers.** Switching from Misskey or Mastodon? Community imports the whole site and keeps the keys and URLs, so followers stay. See [Switch without starting over](#switch-without-starting-over).
- **Set up in the browser.** First start opens a setup wizard (7 languages) where you pick fresh install or migration, your domain and your database. Once it's done, the wizard locks itself.
- **Speaks your language.** The web UI and error messages come in English, 简体中文, 繁體中文, 日本語, Русский, Français and Deutsch.
- **The usual admin toolbox.** Relays, custom emoji management, domain blocks, content filters, S3 / Cloudflare R2 / MinIO storage, a media proxy for remote images, and a Mastodon-style `tootctl` CLI.

## Switch without starting over

Here's the usual reason people never leave their current fediverse software: every account has a cryptographic key pair and a fixed address (its "actor" URL), and every post has a fixed URL too. Remote servers check signatures against those keys. Install different software and the keys and URLs change, signatures fail, and every follow relationship breaks. So admins give up and ask everyone to start from zero or move their accounts one by one.

Community solves this. It imports your **whole Misskey or Mastodon site on the same domain** and keeps:

- each account's **original actor URL and RSA key pair** (and the instance/system actor and its key),
- the **original post URLs** (Misskey `/notes/<id>`, Mastodon `/users/<name>/statuses/<id>`) and old media links,

so remote servers keep trusting you and **your followers stay followers**. Nobody has to run "Move".

**What moves**

- Accounts, profiles, profile fields, avatars and headers, account privacy settings
- Passwords (compatible bcrypt hashes; users log in with their old password)
- Posts, replies, boosts, content warnings, local-only posts, attachments (actual files, checksummed) and supported polls
- Direct messages and their conversations, with recipients kept private
- Follows and pending follow requests, blocks, mutes (with expiry), domain blocks, lists, bookmarks, likes
- Custom emoji and emoji reactions
- Everything else is kept in a private archive, and the dry-run report tells you exactly what was archived

**What doesn't move**

- Login sessions and app (OAuth) tokens: users sign in again
- Two-factor authentication and security keys: if an account has 2FA enabled the dry run stops and tells you, instead of silently switching it off
- Old notifications, and history is not re-sent to other servers
- Some Misskey-only things (certain channel, quote and chat features) may only be archived
- Your DNS: you switch the domain over yourself when you're ready

**Safe to try.** The exporters read your old database in a read-only transaction and never change it, so you can rehearse on a separate Community database and roll back to the old server at any time. Re-running the same import doesn't create duplicates.

**How:** pick "Migrate from Misskey" or "Migrate from Mastodon" in the setup wizard, or use the `community-transfer` CLI. Users can also import personal Misskey/Mastodon backups into their own account. Tested with Misskey 2026.10.0 and Mastodon 4.7.3. Full details, checklist and limits: [docs/data-migration.md](docs/data-migration.md).

## How it compares

A quick, honest overview. Every project here is good at something; pick what fits you.

| | Community | Mastodon | Misskey | GoToSocial | Akkoma |
| --- | --- | --- | --- | --- | --- |
| Language | Go | Ruby + Node.js | TypeScript (Node.js) | Go | Elixir |
| Processes to run | 1 app + Redis | web, streaming, Sidekiq + Redis | app + Redis | 1 app | 1 app |
| Databases | PostgreSQL, MySQL, SQLite | PostgreSQL | PostgreSQL | PostgreSQL, SQLite | PostgreSQL |
| Mastodon client apps | ✅ (see notes) | ✅ native | ❌ | ✅ | ✅ |
| Emoji reactions | ✅ | ❌ | ✅ | ❌ | ✅ |
| Built-in web app | ✅ | ✅ | ✅ | Minimal (use apps) | ✅ |
| Built-in Matrix chat | ✅ | ❌ | ❌ | ❌ | ❌ |
| Web setup wizard | ✅ | ❌ (CLI) | ❌ | ❌ | ❌ (CLI) |
| Import a whole Misskey/Mastodon site on the same domain | ✅ | ❌ | ❌ | ❌ | ❌ |

**If you love GoToSocial for being light:** Community is in the same weight class, and adds a full web app, Misskey-style reactions, Matrix chat and site migration. GoToSocial doesn't need Redis; Community does.

**Good to know before you switch:**

- Editing a post works as "delete and re-post": you get a new post, likes and replies don't carry over. The Mastodon edit endpoint returns `405`, so apps that rely on editing need to adapt.
- Reactions use a Community API extension; plain Mastodon apps show them as normal favourites or not at all.
- It's a young project. See [docs/federation.md](docs/federation.md) for exactly what has been tested against which software.

## Quick start

You need Docker with Compose v2.24 or newer.

```bash
git clone https://github.com/kasuganosora/community.git
cd community
docker compose up -d --build
docker compose logs community | grep -A3 "install token"
```

Then open `http://<your-server>:8080/setup`, paste the token and follow the wizard. For a public server, put an HTTPS reverse proxy in front and use your final domain: it becomes part of every account's identity and shouldn't change later.

More options (manual `.env`, external databases, S3): [docs/setup.md](docs/setup.md) and [docs/configuration.md](docs/configuration.md).

## What you need

| Tier | Hardware | Setup | What you get |
| --- | --- | --- | --- |
| **Minimum** (reduced features) | 1 vCPU, 1 GB RAM | SQLite + Redis, no Meilisearch, a handful of users | Everything works, but no full-text search (falls back to the database), slower media processing and federation backfill |
| **Recommended** | 2 vCPU, 2–4 GB RAM | PostgreSQL + Redis, Meilisearch optional | All features |
| **Medium and up** | Depends on users and federation traffic | | See [docs/tuning.md](docs/tuning.md) |

What we measured (in Docker with hard CPU/memory limits and swap disabled, October 17, 2026, 10 users and 8 concurrent clients doing timelines, posts, polls, media uploads and likes for 10 minutes):

- **Minimum tier** (whole stack capped at 1 CPU / 1 GB): Community used 78 MiB idle and 196 MiB at peak, Redis under 10 MiB. No out-of-memory kills. About 97 requests/s, median 71 ms, 95th percentile 198 ms.
- **Recommended tier** (PostgreSQL + Redis + Meilisearch, 2 CPUs): Community peaked at 215–250 MiB, PostgreSQL ~300 MiB, Redis ~30 MiB. With a 2 GB total, Meilisearch (capped at 704 MiB) was killed for lack of memory while Community kept running and search fell back to the database; with 4 GB everything stayed up and Meilisearch peaked at 733 MiB. **If you want Meilisearch, plan for 4 GB** (or give it at least 1 GB of its own).

On the demo server (October 2026) Community uses about 82 MiB, Redis 141 MiB and Meilisearch 932 MiB, so Meilisearch is the big one, which is why it's optional. Raw numbers and how to rerun them: [docs/benchmarks.md](docs/benchmarks.md). Video thumbnails need FFmpeg (included in the Docker image).

## Works with

- **Fediverse:** tested with Misskey and Mastodon (follows, replies, DMs, deletes, custom emoji, reactions, media). ActivityPub relays are supported too.
- **Matrix:** tested with Synapse and Element Web, including encrypted rooms and attachments.
- **Mastodon apps:** verified with **Toot! (iOS)** on the demo (login and timelines, October 2026). Community implements the Mastodon client API, so most Mastodon apps should work too (keep the editing caveat above in mind).
- **Matrix apps:** verified with Element Web.

Tested versions and known limits: [docs/federation.md](docs/federation.md).

## Upgrading

`git pull && docker compose up -d --build`. Database migrations run on start. **Back up your database and `uploads/` first.** See [docs/setup.md](docs/setup.md#upgrading-an-existing-deployment-ops).

## Documentation

- [Setup wizard & configuration file](docs/setup.md)
- [Installation & configuration](docs/configuration.md)
- [Data migration](docs/data-migration.md)
- [Feature details](docs/features.md)
- [Federation & compatibility](docs/federation.md)
- [Admin console](docs/admin-console.md) · [tootctl](docs/tootctl.md)
- [Tuning](docs/tuning.md) · [Benchmarks](docs/benchmarks.md)
- [Architecture & code map](docs/architecture.md) · [Development & testing](docs/development.md)
- Deep dives: [Feed runtime](docs/feed-runtime.md), [Recommendations](docs/recommendations.md), [Link previews](docs/link-previews.md), [Matrix web groups](docs/matrix-web-groups.md), [Matrix ↔ ActivityPub DMs](docs/matrix-activitypub-direct.md)

## Contributing

Issues and pull requests are welcome at [GitHub Issues](https://github.com/kasuganosora/community/issues). For compatibility bugs, please include the client or server version, steps to reproduce and redacted logs. To build and run tests locally, see [docs/development.md](docs/development.md).

## License

No license has been chosen yet. Until a `LICENSE` file is added, all rights are reserved.

## Acknowledgements

Community stands on the shoulders of [Mastodon](https://github.com/mastodon/mastodon), [Misskey](https://github.com/misskey-dev/misskey), [GoToSocial](https://github.com/superseriousbusiness/gotosocial), [Akkoma](https://akkoma.dev/AkkomaGang/akkoma), the [Matrix](https://matrix.org) specification and [Synapse](https://github.com/element-hq/synapse)/[Element](https://element.io), and the ActivityPub community. Thank you.
