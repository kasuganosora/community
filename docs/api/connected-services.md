# Personal connected services

Open **Settings → Connected services** to create personal access tokens, revoke
app authorizations, send API requests, and configure Webhooks. Management APIs
under `/api/v1/settings/tokens` and `/api/v1/settings/webhooks` require a web
login session; delegated OAuth tokens cannot create more credentials or hooks.

## Access tokens

`POST /api/v1/settings/tokens` accepts `name`, `scopes` (`read`, `write`, or both),
and `expires_in_days` (`7`, `30`, `90`, `365`, or `0` for no expiry). These are
the existing coarse OAuth scopes, not administrator privileges. The response
contains `item` and a `token` shown only once. Only its SHA-256 digest is stored.
`GET` lists metadata, and `DELETE /:id` immediately revokes a token. Each user
can retain up to 50 tokens, including expired tokens until removed.

The API console accepts only this server's `/api/` paths. A pasted token stays
in memory; leaving the page or switching accounts clears it. HTTP errors from
the console do not sign out the active account.

### Matrix access with the same token (bots)

There is one token system. A personal access token (or an OAuth access token
whose app requested the scopes) can also call this server's Matrix
client-server API (`/_matrix/client/**`, `/_matrix/media/**`) as its owner
when the Matrix permissions are ticked in Settings → Access tokens:

| Scope | Allows |
|-------|--------|
| `read:matrix` | `GET`/`HEAD` requests incl. `/sync`, plus read-only `POST`s: `/keys/query`, `/search`, `/user_directory/search`, `/publicRooms`, `/user/{id}/filter` |
| `write:matrix` | every other request: sending messages, joins/leaves, room management, `/keys/upload`, to-device, ... |

Bots normally need both. They are never implied by `read`, `write`,
`follow` or "Enable all", and existing tokens do not get them. Editing the
scopes keeps the token string; removing them takes effect on the next request.

Use the token exactly like a Matrix access token
(`Authorization: Bearer <token>`; `?access_token=` works too and is checked
the same way). No `/login` is needed. Without a Matrix scope the server
answers `401 M_UNKNOWN_TOKEN`; with only `read:matrix`, mutating requests get
`403 M_FORBIDDEN`.

Each token is bound to one dedicated Matrix device. Its `device_id` is
derived from the token (`MTOK` + 12 hex chars, stable for the token's
lifetime) and its display name is the app name (or "API token"), so `/sync`
positions, to-device messages and E2EE keys persist; `/account/whoami`
returns `user_id` and `device_id`. The device is created on first use.

- `POST /logout` with such a token deletes only the bound device session
  (keys, pending to-device messages); the API token stays valid and the next
  request starts a fresh, empty device. `POST /logout/all` logs out all of the
  user's Matrix devices but never revokes API tokens. Revoke the token in
  Settings to cut off access; its device is removed on the next attempt to use it.
- Revoked or expired tokens and suspended accounts are rejected immediately.
- The Matrix admin APIs (`/_synapse/admin/**`, `/_matrix/client/v3/admin/**`) and appservice tokens still
  require native Matrix credentials; federation is unaffected.

## Webhooks

`POST /api/v1/settings/webhooks` accepts:

```json
{
  "name": "My notifications",
  "url": "https://receiver.example/hooks/community",
  "secret": "",
  "events": ["note", "reply", "mention"],
  "enabled": true
}
```

An empty secret generates a random secret on creation; it retains the current
secret on `PUT /:id`. The generated secret is returned once. `GET` never returns
secrets. The limit is 10 hooks per account. Destinations must be public HTTPS
URLs; DNS and connection checks also reject private destinations. Redirects
are not followed.

| Event | Trigger |
| --- | --- |
| `follow` | This account follows someone or sends a follow request |
| `followed` | This account receives a follow notification |
| `note` | This account publishes a post |
| `noteUpdated` | This account's post changes |
| `noteDeleted` | This account deletes a post |
| `reply` | This account receives a mention notification for a reply to its post |
| `renote` | This account receives a boost notification |
| `reaction` | This account receives a favourite or emoji reaction notification |
| `mention` | This account receives a mention notification |

A reply can trigger both `reply` and `mention` when both are selected.
Events belong only to the hook owner, including the owner's private posts.
Historical data imports do not replay events. Events are inserted into a
durable delivery queue in the source database transaction.

Example delivery:

```json
{
  "id": "delivery-uuid",
  "hookId": "subscription-uuid",
  "type": "mention",
  "createdAt": "2026-10-18T00:00:00Z",
  "body": {"notificationId": "123", "userId": "45", "noteId": "67"}
}
```

Post events contain `noteId` and `userId`; follow events contain `userId`.
Identifiers are strings. Payloads intentionally omit post bodies and profile
data. Fetch current details using an appropriately scoped token through
`GET /api/v1/statuses/:id`, `/notifications/:id`, or `/accounts/:id`.
This is Community's event contract, not a guarantee of Misskey wire compatibility.

Requests carry `X-Community-Event`, `X-Community-Delivery`,
`X-Community-Timestamp`, `X-Community-Signature`, and `Idempotency-Key`.
Verify the signature over the **unmodified request bytes**:

```python
expected = "sha256=" + hmac.new(
    secret.encode(),
    timestamp.encode() + b"." + delivery_id.encode() + b"." + raw_body,
    hashlib.sha256,
).hexdigest()
valid = hmac.compare_digest(expected, received_signature)
```

Also validate the timestamp against a short clock-skew window and deduplicate
by delivery ID. Delivery is at least once. Any 2xx acknowledges it; failures
retry up to five attempts with increasing delays. Each attempt times out after
25 seconds. Completed payloads are cleared; delivery metadata is kept 30 days.

`GET /:id/deliveries` shows the last 20 deliveries. `POST /:id/test` queues a
connectivity test, limited to once per minute. Disabling or changing a hook's
destination/secret cancels queued deliveries; an HTTP request already in flight
may finish. `DELETE /:id` removes the hook and its records.
