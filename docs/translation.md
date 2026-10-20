# Translation

Policy settings are stored in the database (Admin → External services →
Translation). `.env` only carries connection settings
(`TRANSLATION_PROVIDER`, `LIBRETRANSLATE_URL`, `LIBRETRANSLATE_API_KEY`) and
the LLM provider list.

| Setting | Meaning | Default |
|---|---|---|
| `config.translations.mode` | Primary engine: `traditional` or `llm` | `traditional` |
| `config.translations.fallback_enabled` | Retry failures with the other engine | on for an LLM primary, off for a traditional primary |
| `config.translations.llm_allow_private` | Send followers-only/direct posts and non-public group messages to the LLM | off |
| `config.translations.group_messages` | Offer Translate on group chat messages | on only when the primary is traditional at an explicit self-hosted LibreTranslate-compatible URL |

## Engine order

* LLM primary → traditional fallback: on unusable output, provider error or
  timeout the explicit self-hosted traditional backend is tried (the public
  default instance never is). Limits/quota errors never fall back.
* Traditional primary → LLM fallback: on errors/timeouts, empty output or
  output identical to the input while the languages differ, the LLM is tried.

The `engine` response field reports which engine produced the result.
Privacy is checked before any content reaches an LLM, whether it is the
primary or the fallback engine.

## Group chat messages

`POST /api/v1/chat/groups/:id/messages/:messageID/translate` with
`{"lang": "ja"}`. Only room members who can read the event (history
visibility) may translate it. Group messages are not public posts: they may
be sent to an LLM only when the room is public (public join rule or
world-readable history) or `llm_allow_private` is on; otherwise the LLM engine
is refused with `translation_visibility_restricted` (a traditional engine may
still serve the request). Rate limits and quotas are shared with posts.

The translation cache is keyed by backend, target language and content hash;
`translation_cache_group_refs` links event IDs to hashes so redactions purge
cached translations and a later translation of an edited message drops
translations of the previous version.
