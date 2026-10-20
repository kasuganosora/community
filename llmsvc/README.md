# llmsvc

LLM integration for Community: provider configuration, usage/cost recording
and LLM-backed status translation.

- **Client library**: ThinkBot's `llm` package
  (`github.com/kasuganosora/thinkbot/llm` and its `openai`, `anthropic`,
  `google`, `grok` subpackages) is imported directly as a Go module
  dependency, pinned in `go.mod` (master `3d8a25b`). It brings its retries,
  unified `*llm.LLMError` classification, `StatsRecordingProvider` and
  `ComputeCost`. Update with `go get github.com/kasuganosora/thinkbot@<commit>`.
- **Configuration** lives in `system_settings` under `config.llm.config`
  (JSON object: providers + service → provider mapping). Nothing is read
  from `.env`.
- **API keys** are AES-encrypted with a key derived from `ENCRYPTED_KEY`
  plus a random per-key salt (`utils.AesEncrypt` / `utils.GetEncryptKey`).
  Admin APIs return only `api_key_set` and a mask; keys are write-only and are
  never logged. Provider error texts are reduced to stable codes.
- **Usage**: every call is aggregated per UTC day / feature / provider /
  model in `llm_usage_daily` (calls, errors, tokens, cost from configured
  per-1M-token prices). `llm_user_usage_daily` counts per-account calls for
  the per-user daily quota.
- **Translation** (`TranslateWithLLM`): mentions, hashtags, links, custom
  emoji (`:shortcode:` and `<img>`), inline/block code and URLs are replaced
  by `{{n}}` placeholders before the request and restored afterwards (dropped
  placeholders are appended, so nothing is lost). Content warnings, poll
  options and media descriptions are translated in the same request. Results
  are cached in `translation_caches` keyed by a hash of the translated input,
  target language and backend, shared across statuses.

## Chat endpoint of OpenAI-compatible providers

`chat_path` (admin: External services > LLM > Chat path) overrides the Chat
Completions path; it may be a path relative to the base URL or a full endpoint
URL. Empty = automatic (`ResolveChatPath`, mirroring ThinkBot's
`config.resolveChatPath`): a base URL ending in a version segment (`/v1`, `/v4`,
`/api/paas/v4`, `/v1beta`) gets `/chat/completions`, otherwise
`/v1/chat/completions`. The connection test reports the resolved `endpoint`
(no key, no query string).

## Translation fallback

In LLM mode an unusable answer (`llm_empty_output`, `llm_refusal`,
`llm_length_ratio`, `llm_unchanged_output`, `llm_bad_output`) or a provider
error (`llm_timeout`, `llm_unreachable`, ...) falls back to the traditional
backend only if `LIBRETRANSLATE_URL` is explicitly set to a non-default URL
(never the public libretranslate.de). Each fallback is logged with its reason.
The response carries `provider` and `engine` (`llm` | `traditional`). Without a
fallback the error is `translation_failed` (422) or `translation_unavailable`
(424), never a 5xx.
