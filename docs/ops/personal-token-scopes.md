# Personal access token scopes (editable, DB-authoritative)

Personal access tokens (Settings > Connected services) can be renamed and
have their permissions changed after creation; the token string never
changes. Scopes are resolved at request time:

| `personal_access_tokens.scope_source` | Used at request time | Notes |
|---|---|---|
| `edit` | `scope_bits` | set by create (new tokens) and by the edit endpoint |
| `blob` | `scope_bits` (= blob) | copied once from the token itself on first use |
| `migration` | **token blob** | derived from `scopes` by the migration; never overrides the blob, a difference is logged |
| empty / `scope_bits` NULL | token blob | filled from the blob on the next use (source `blob`) |

Empty or unreadable DB values are never treated as "no scopes" or "all
scopes". OAuth app tokens (e.g. Toot!) and web session tokens have no
per-token DB scopes and keep using the scopes in their token; the server only
logs (once per token per process) an OAuth token whose scopes exceed its
application's registered scopes. Mismatch warnings contain token / user /
app ids and scope bits, never the token.

Log lines to look for after deploy:

    personal token <id> user <uid>: scope mismatch db[migration]{...} blob{...} using blob
    oauth token <hash prefix> user <uid> app <id> (<name>): token scopes {...} exceed app scopes {...}; token scopes kept

## Read-only checks (PostgreSQL)

```sql
-- Personal tokens by scope source, live (not revoked) only.
SELECT p.scope_source, (p.scope_bits IS NULL) AS no_bits, count(*)
FROM personal_access_tokens p JOIN auth_tokens a ON a.token = p.token_hash
WHERE a.revoke IS NULL GROUP BY 1, 2 ORDER BY 1, 2;

-- Tokens with empty / odd scope columns (no secrets selected).
SELECT p.id, p.user_id, p.name, p.scopes, p.scope_bits, p.scope_source, a.created_at, a.last_used_at
FROM personal_access_tokens p JOIN auth_tokens a ON a.token = p.token_hash
WHERE a.revoke IS NULL AND (
  p.scopes IS NULL OR p.scopes::text IN ('null', '[]')
  OR p.scope_bits IS NULL OR p.scope_bits IN ('', 'null', '{}')
  OR p.scope_source NOT IN ('edit', 'blob', 'migration')
  OR p.scope_bits LIKE '%"read_scope":0,"write_scope":0,"admin_scope":0,"other_scope":0%')
ORDER BY a.created_at;

-- Personal token metadata rows without an auth token, and vice versa.
SELECT p.id, p.user_id, p.name FROM personal_access_tokens p
LEFT JOIN auth_tokens a ON a.token = p.token_hash WHERE a.token IS NULL;
SELECT left(a.token, 12) AS token_hash_prefix, a.user_id, a.created_at FROM auth_tokens a
LEFT JOIN personal_access_tokens p ON p.token_hash = a.token
WHERE a.external_client = 'community:personal-access-token' AND a.revoke IS NULL AND p.id IS NULL;
```

Personal tokens have `auth_tokens.external_client = 'community:personal-access-token'`.
