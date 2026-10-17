# tootctl

Community ships a Mastodon-style `tootctl` admin CLI (`cmd/tootctl`), including accounts, domains, emoji, media, relays, settings, OAuth apps, passkeys, feeds and more.

Full command reference: [cmd/README.md](../cmd/README.md).

```bash
docker compose exec community tootctl --help
docker compose exec community tootctl accounts create admin --email admin@example.com --confirmed --admin
```
