import { createI18n } from 'vue-i18n'
import zhCN from '@/locales/zh-CN.json'
import { setFormatLocale } from '@/shared/utils/format'

/**
 * UI localisation. zh-CN is the source language and is always bundled; the
 * active locale (if different) is fetched before any component module runs,
 * so strings computed at module scope are already in the right language.
 * Switching language persists the choice and reloads the page.
 */
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
export const LOCALE_STORAGE_KEY = 'ui-locale'

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

export function detectLocale(): Locale {
  try {
    const saved = localStorage.getItem(LOCALE_STORAGE_KEY)
    if (isLocale(saved)) return saved
  } catch { /* storage unavailable */ }
  const langs = typeof navigator !== 'undefined' ? (navigator.languages?.length ? navigator.languages : [navigator.language]) : []
  for (const l of langs) {
    const m = matchLocale(l)
    if (m) return m
  }
  return SOURCE_LOCALE
}

const loaders = import.meta.glob<{ default: Record<string, unknown> }>(['@/locales/*.json', '!@/locales/zh-CN.json'])
async function loadMessages(locale: Locale): Promise<Record<string, unknown> | null> {
  const loader = Object.entries(loaders).find(([p]) => p.endsWith(`/${locale}.json`))?.[1]
  return loader ? (await loader()).default : null
}

// Russian: one / few / many; vue-i18n's default rule only knows "1 vs. other".
function ruRule(choice: number, choicesLength: number): number {
  const n = Math.abs(choice)
  if (choicesLength < 3) return n === 1 ? 0 : 1
  const n10 = n % 10, n100 = n % 100
  if (n10 === 1 && n100 !== 11) return 0
  if (n10 >= 2 && n10 <= 4 && (n100 < 12 || n100 > 14)) return 1
  return 2
}
// French: 0 and 1 are singular.
function frRule(choice: number, choicesLength: number): number {
  if (choicesLength < 2) return 0
  return Math.abs(choice) < 2 ? 0 : 1
}
// English / German: only 1 is singular (vue-i18n's default treats 0 specially).
function oneOtherRule(choice: number, choicesLength: number): number {
  if (choicesLength < 2) return 0
  return Math.abs(choice) === 1 ? 0 : 1
}

const initial = detectLocale()

export const i18n = createI18n({
  legacy: false,
  globalInjection: true,
  locale: initial,
  fallbackLocale: ['zh-CN', 'en'],
  messages: { 'zh-CN': zhCN } as Record<string, typeof zhCN>,
  pluralRules: { ru: ruRule, fr: frRule, en: oneOtherRule, de: oneOtherRule },
  missingWarn: false,
  fallbackWarn: false,
})

if (initial !== SOURCE_LOCALE) {
  try {
    const m = await loadMessages(initial)
    if (m) i18n.global.setLocaleMessage(initial, m as typeof zhCN)
  } catch (e) {
    console.warn('Failed to load locale', initial, e)
  }
}
if (initial !== 'en' && initial !== SOURCE_LOCALE) {
  // Second fallback; loaded lazily, missing keys fall back to zh-CN meanwhile.
  void loadMessages('en').then(m => { if (m) i18n.global.setLocaleMessage('en', m as typeof zhCN) }).catch(() => {})
}
if (typeof document !== 'undefined') document.documentElement.lang = initial
setFormatLocale(initial)

/**
 * Make argument-less toLocaleString()/toLocaleDateString()/toLocaleTimeString()
 * (and `[]`) follow the UI language rather than the browser default, so dates
 * and numbers rendered across the app match the chosen language.
 */
function defaultLocale<T extends (...a: never[]) => string>(proto: object, name: string): void {
  const p = proto as Record<string, T>
  const orig = p[name]
  if (!orig || (orig as unknown as { __i18n?: boolean }).__i18n) return
  const wrapped = function (this: unknown, locales?: unknown, ...rest: unknown[]) {
    const l = locales === undefined || (Array.isArray(locales) && locales.length === 0) ? initial : locales
    return (orig as unknown as (...a: unknown[]) => string).call(this, l, ...rest)
  }
  ;(wrapped as unknown as { __i18n: boolean }).__i18n = true
  p[name] = wrapped as unknown as T
}
for (const name of ['toLocaleString', 'toLocaleDateString', 'toLocaleTimeString']) defaultLocale(Date.prototype, name)
defaultLocale(Number.prototype, 'toLocaleString')

export function currentLocale(): Locale {
  return (i18n.global.locale as unknown as { value: string }).value as Locale
}

/**
 * Translate. Positional args fill {0}, {1}…; the first numeric arg selects
 * the plural branch for messages written as "one | other" (or ru's 3 forms).
 */
export function t(key: string, args?: unknown[]): string {
  const g = i18n.global
  if (!args) return g.t(key)
  const n = args.find(a => typeof a === 'number') as number | undefined
  return n === undefined ? g.t(key, args) : g.t(key, args, n)
}

/** Persist and apply a new UI language (reloads so module-level strings refresh). */
export function setLocale(locale: Locale, opts: { reload?: boolean } = {}): void {
  try { localStorage.setItem(LOCALE_STORAGE_KEY, locale) } catch { /* private mode */ }
  if (locale === currentLocale()) return
  if (opts.reload !== false && typeof window !== 'undefined') window.location.reload()
}
