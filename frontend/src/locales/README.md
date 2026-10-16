# UI translations

- `zh-CN.json` is the source language; every other file must have exactly the
  same keys (enforced by `src/shared/i18n/__tests__/locales.spec.ts`).
- English is the default UI language (no saved choice and no supported browser
  language) and the first fallback for a missing key, then zh-CN.
- Keys are `<domain|shared>.<ComponentFile>.<id>`; `apiError.*` holds the
  localized versions of backend error/message strings. The server answers in
  English; `shared/i18n/apiErrors.ts` maps each English wording (plus older
  Chinese wordings and a few patterns with `{0}` captures) to its key. When a
  backend message is added or reworded, update that table and every locale; the
  locale tests fail on keys used by the table but missing from the files.
- Arguments are positional: `{0}`, `{1}`. Literal `{ } @ | $ %` must be written
  as `{'{'}`, `{'@'}` etc. (vue-i18n message syntax).
- Plurals: when the first numeric argument is a count, write
  `"{0} post | {0} posts"` (Russian uses three forms: one | few | many).
  Chinese and Japanese use a single form.
- In templates use `$t('key', [args])`; in scripts `import { t } from '@/shared/i18n'`.
- `scripts/i18n/extract.mjs` is the codemod used for the initial extraction of
  hard-coded Chinese strings; it can be re-run to catch new ones.
