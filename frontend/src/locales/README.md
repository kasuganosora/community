# UI translations

- `zh-CN.json` is the source language; every other file must have exactly the
  same keys (enforced by `src/shared/i18n/__tests__/locales.spec.ts`).
- Keys are `<domain|shared>.<ComponentFile>.<id>`; `apiError.*` holds the
  localized versions of backend error strings (see `shared/i18n/apiErrors.ts`).
- Arguments are positional: `{0}`, `{1}`. Literal `{ } @ | $ %` must be written
  as `{'{'}`, `{'@'}` etc. (vue-i18n message syntax).
- Plurals: when the first numeric argument is a count, write
  `"{0} post | {0} posts"` (Russian uses three forms: one | few | many).
  Chinese and Japanese use a single form.
- In templates use `$t('key', [args])`; in scripts `import { t } from '@/shared/i18n'`.
- `scripts/i18n/extract.mjs` is the codemod used for the initial extraction of
  hard-coded Chinese strings; it can be re-run to catch new ones.
