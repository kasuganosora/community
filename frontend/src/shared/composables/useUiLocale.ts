import client from '@/shared/api/client'
import { currentLocale, isLocale, LOCALE_STORAGE_KEY, setLocale, type Locale } from '@/shared/i18n'

/**
 * Web UI language, kept in localStorage (guests, first paint) and mirrored to
 * the signed-in account as the "interface:locale" preference.
 */
function hasLocalChoice(): boolean {
  try { return isLocale(localStorage.getItem(LOCALE_STORAGE_KEY)) } catch { return false }
}

/** Pick a language: save it on the account when signed in, then reload. */
export async function chooseUiLocale(locale: Locale, signedIn: boolean): Promise<void> {
  if (signedIn) {
    try { await client.patch('/api/v1/preferences', { 'interface:locale': locale }) } catch { /* local choice still applies */ }
  }
  setLocale(locale)
}

/**
 * After sign-in: adopt the account's language, or upload this device's
 * explicit choice if the account has none yet.
 */
export async function syncUiLocale(): Promise<void> {
  try {
    const { data } = await client.get<Record<string, unknown>>('/api/v1/preferences')
    const saved = data?.['interface:locale']
    if (isLocale(saved)) {
      if (saved !== currentLocale()) setLocale(saved)
    } else if (hasLocalChoice()) {
      await client.patch('/api/v1/preferences', { 'interface:locale': currentLocale() })
    }
  } catch { /* keep the local value */ }
}
