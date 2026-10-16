// Locale list and matching helpers, free of message catalogues so small
// entry points (the setup wizard) can use them without the full bundles.
export const LOCALES = [
  { code: 'zh-CN', name: '简体中文' },
  { code: 'zh-TW', name: '繁體中文' },
  { code: 'ja', name: '日本語' },
  { code: 'en', name: 'English' },
  { code: 'ru', name: 'Русский' },
  { code: 'fr', name: 'Français' },
  { code: 'de', name: 'Deutsch' },
] as const
export type Locale = typeof LOCALES[number]['code']
export const SOURCE_LOCALE: Locale = 'zh-CN'
/** Used when nothing is saved and no browser language matches. */
export const DEFAULT_LOCALE: Locale = 'en'
/** Missing keys resolve in this order. */
export const FALLBACK_LOCALES: Locale[] = ['en', 'zh-CN']
export const LOCALE_STORAGE_KEY = 'ui-locale'
/** The instance's default language (GET /api/v2/instance default_locale), cached for the next load. */
export const INSTANCE_LOCALE_STORAGE_KEY = 'instance-default-locale'

const CODES = LOCALES.map(l => l.code) as readonly string[]

/** Map a BCP 47 tag (navigator.languages, Accept-Language) onto a supported locale. */
export function matchLocale(tag: string | null | undefined): Locale | null {
  if (!tag) return null
  const lower = tag.trim().toLowerCase().replace('_', '-')
  if (!lower) return null
  if (lower.startsWith('zh')) {
    return /^zh-(tw|hk|mo|hant)/.test(lower) ? 'zh-TW' : 'zh-CN'
  }
  const base = lower.split('-')[0]
  return (CODES.includes(base) ? base : null) as Locale | null
}

export function isLocale(v: unknown): v is Locale {
  return typeof v === 'string' && CODES.includes(v)
}

/** Pick the first supported browser language, else English. */
export function localeFromLanguages(langs: readonly (string | null | undefined)[]): Locale {
  for (const l of langs) {
    const m = matchLocale(l)
    if (m) return m
  }
  return DEFAULT_LOCALE
}

export function detectLocale(): Locale {
  try {
    const saved = localStorage.getItem(LOCALE_STORAGE_KEY)
    if (isLocale(saved)) return saved
  } catch { /* storage unavailable */ }
  return browserLocale() ?? instanceLocale() ?? DEFAULT_LOCALE
}

/** First supported browser language, or null when none matches. */
export function browserLocale(): Locale | null {
  const langs = typeof navigator !== 'undefined' ? (navigator.languages?.length ? navigator.languages : [navigator.language]) : []
  for (const l of langs) {
    const m = matchLocale(l)
    if (m) return m
  }
  return null
}

function instanceLocale(): Locale | null {
  try {
    const v = localStorage.getItem(INSTANCE_LOCALE_STORAGE_KEY)
    return isLocale(v) ? v : null
  } catch { return null }
}
