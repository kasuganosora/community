import { createI18n } from 'vue-i18n'
import { LOCALES, isLocale, localeFromLanguages, LOCALE_STORAGE_KEY, type Locale } from '@/shared/i18n/locales'
import zhCN from './locales/zh-CN.json'
import zhTW from './locales/zh-TW.json'
import ja from './locales/ja.json'
import en from './locales/en.json'
import ru from './locales/ru.json'
import fr from './locales/fr.json'
import de from './locales/de.json'

export { LOCALES }
export type { Locale }

// The wizard bundles its own small catalogues so switching language is
// instant and the main app's message files are not downloaded.
export const messages = { 'zh-CN': zhCN, 'zh-TW': zhTW, ja, en, ru, fr, de }

export function initialLocale(): Locale {
  try {
    const saved = localStorage.getItem(LOCALE_STORAGE_KEY)
    if (isLocale(saved)) return saved
  } catch { /* storage unavailable */ }
  const langs = typeof navigator !== 'undefined' ? (navigator.languages?.length ? navigator.languages : [navigator.language]) : []
  return localeFromLanguages(langs)
}

export const i18n = createI18n({ legacy: false, locale: initialLocale(), fallbackLocale: 'en', messages })

export function applyLocale(locale: Locale): void {
  i18n.global.locale.value = locale
  document.documentElement.lang = locale
  try { localStorage.setItem(LOCALE_STORAGE_KEY, locale) } catch { /* storage unavailable */ }
}
