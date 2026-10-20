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
* Traditional primary → LLM fallback: on errors/timeouts or unusable output
  (see below), the LLM is tried. Without a fallback the request fails with
  422 `translation_failed`.

The `engine` response field reports which engine produced the result.

### Unusable traditional output

Traditional output is rejected — never shown, never cached — when it is:

* empty while the input has text (`empty_output`);
* the input echoed back (`unchanged_output`): compared after NFKC
  (fullwidth/halfwidth forms fold together), lower-casing, and dropping
  mentions, URLs, hashtags, punctuation, symbols, emoji and whitespace — so a
  model that only turned `,` into `，` is caught;
* not in the target's script (`wrong_script`): for targets whose script is
  cheap to check (zh/ja/ko, Cyrillic, Latin-script languages), when the input
  is mostly not in that script, the output has at least 8 letters and fewer
  than 20 % of them are in it.

### Source language and labels

Remote servers' language tags are untrusted (client defaults mislabel many
posts). For remote posts community sends `source=auto` and the tag as
`source_hint`; mt-gateway never gives hy-mt a source language and, when it
falls back to LibreTranslate/Argos, uses LibreTranslate's detection first and
the hint only if detection fails. Local posts send their declared language.

The label shows the language the backend reports in `detectedLanguage`
("an unknown language" when none is reported for a remote post) and the
engine it reports in `engine` (`hy-mt` → "Hunyuan MT (local)",
`libretranslate`, `mixed`), falling back to the provider name for backends
that report none.

### Error codes

| Code | HTTP | Meaning |
|---|---|---|
| `translation_failed` | 422 | backend output was unusable (echo/wrong script), or mt-gateway refused it (`translation_unchanged`) |
| `translation_language_unsupported` | 422 | no model for this language pair (mt-gateway `translation_language_unsupported`, or LibreTranslate 400 "… is not supported") |
| `translation_unavailable` | 503 | backend outage |

None of these is cached. With a fallback engine enabled, the first two are
retried with the LLM first.

### Purging cached bad translations

    tootctl cache purge-translations --dry-run   # list
    tootctl cache purge-translations             # delete

Removes cache entries whose output fails the checks above. The source text is
recovered from a status referencing the entry; group-chat entries (no status)
are judged by output script alone (CJK targets). The command needs only the
database (no running server); affected posts are translated afresh on the next
request.
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
