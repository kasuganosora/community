# Account migration and deletion

Open **Settings → More** for account migration and permanent deletion. These
operations require a web session. Moving or deleting an account also requires
its current password and explicit confirmation. Delegated API tokens cannot
perform them.

## Migration

To receive followers from another account, add that account under **Move from
another account**, save, then request the move on the old server. The saved
actor URLs are published as ActivityPub `alsoKnownAs` aliases.

To move away, first add the current account as an alias on the destination
server. Enter the destination address and password under **Move to another
account**. Remote aliases are fetched again before authorizing the move.
Community records `movedTo`, transfers eligible local followers, and queues an
ActivityPub Move for remote followers. Delivery survives server restarts.
Remote servers process the move asynchronously.

Migration transfers followers. Existing posts, uploaded files, and followed
accounts stay on the old account; personal data import/export is separate.
A completed move cannot be undone through this interface.

## Permanent deletion

Deletion requires the username, password, and confirmation. The final active
administrator cannot delete their account until another administrator exists.

The request immediately disables authentication, revokes web/OAuth and native
Matrix sessions, hides profile details, and disables personal Webhooks. A
durable background job then removes posts, uploads, messages sent by the
account, personal settings, credentials, and private queued data. Deletion
proceeds in batches, resumes after restart, and retries failures. The server
logs failures under `Account erasure`; the database `account_deletions` row
records progress and the last error.

The username, actor identity, deletion state, and federation signing material
are retained as a minimal tombstone so queued ActivityPub Delete requests can
still be delivered. Data already copied by remote servers or recipients is
outside the local server's control.

## Internal endpoints

All paths below are relative to `/api/v1/accounts/lifecycle`:

| Method | Path | Body |
| --- | --- | --- |
| GET | `/` | Current aliases and migration/deletion state |
| PUT | `/aliases` | `aliases`: up to 10 account addresses or actor URLs |
| POST | `/move` | `target`, `password`, `confirm: true` |
| POST | `/delete` | `password`, `confirmation`: username, `confirm: true` |

Successful deletion returns HTTP 202; completion happens in the background.
