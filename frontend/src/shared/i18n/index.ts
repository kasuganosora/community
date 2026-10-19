import { createI18n } from 'vue-i18n'
import zhCN from '@/locales/zh-CN.json'
import en from '@/locales/en.json'
import { setFormatLocale } from '@/shared/utils/format'

/**
 * UI localisation. zh-CN is the source language (the one strings are written
 * in); English is the default for visitors whose browser asks for none of the
 * supported languages and the first fallback for a missing key. Both are
 * bundled; any other active locale is fetched before any component module
 * runs, so strings computed at module scope are already in the right language.
 * Switching language persists the choice and reloads the page.
 */
import { INSTANCE_LOCALE_STORAGE_KEY, browserLocale, LOCALES, SOURCE_LOCALE, DEFAULT_LOCALE, FALLBACK_LOCALES, LOCALE_STORAGE_KEY, matchLocale, isLocale, localeFromLanguages, detectLocale, type Locale } from './locales'
export { LOCALES, SOURCE_LOCALE, DEFAULT_LOCALE, FALLBACK_LOCALES, LOCALE_STORAGE_KEY, matchLocale, isLocale, localeFromLanguages, detectLocale }
export type { Locale }

const loaders = import.meta.glob<{ default: Record<string, unknown> }>(['@/locales/*.json', '!@/locales/zh-CN.json', '!@/locales/en.json'])
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
  fallbackLocale: FALLBACK_LOCALES,
  messages: { 'zh-CN': zhCN, en: en as typeof zhCN } as Record<string, typeof zhCN>,
  pluralRules: { ru: ruRule, fr: frRule, en: oneOtherRule, de: oneOtherRule },
  missingWarn: false,
  fallbackWarn: false,
})

if (initial !== SOURCE_LOCALE && initial !== 'en') {
  try {
    const m = await loadMessages(initial)
    if (m) i18n.global.setLocaleMessage(initial, m as typeof zhCN)
  } catch (e) {
    console.warn('Failed to load locale', initial, e)
  }
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
 * A named-argument object such as { count: 3 } fills {count}; its `count`
 * (or first numeric value) selects the plural branch.
 */
export function t(key: string, args?: unknown[] | Record<string, unknown>): string {
  const g = i18n.global
  if (args == null) return g.t(key)
  if (!Array.isArray(args)) {
    if (typeof args !== 'object') return g.t(key, [args])
    const named = args as Record<string, unknown>
    const n = typeof named.count === 'number' ? named.count : Object.values(named).find(a => typeof a === 'number') as number | undefined
    return n === undefined ? g.t(key, named) : g.t(key, named, n)
  }
  const n = args.find(a => typeof a === 'number') as number | undefined
  return n === undefined ? g.t(key, args) : g.t(key, args, n)
}

/** Persist and apply a new UI language (reloads so module-level strings refresh). */
export function setLocale(locale: Locale, opts: { reload?: boolean } = {}): void {
  try { localStorage.setItem(LOCALE_STORAGE_KEY, locale) } catch { /* private mode */ }
  if (locale === currentLocale()) return
  if (opts.reload !== false && typeof window !== 'undefined') window.location.reload()
}

/**
 * Remember the instance's default language (set by the setup wizard). It is
 * used only when the visitor chose no language and the browser asks for none
 * of the supported ones; then the page reloads once to apply it.
 */
export function applyInstanceDefaultLocale(value: unknown): void {
  if (!isLocale(value)) return
  try {
    if (localStorage.getItem(INSTANCE_LOCALE_STORAGE_KEY) === value) return
    localStorage.setItem(INSTANCE_LOCALE_STORAGE_KEY, value)
    if (isLocale(localStorage.getItem(LOCALE_STORAGE_KEY)) || browserLocale()) return
  } catch { return }
  if (value !== currentLocale() && typeof window !== 'undefined') window.location.reload()
}
