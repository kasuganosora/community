import { i18n, t } from '@/shared/i18n'

/**
 * Prefer the stable error code the API returns ("code": "FILTER_KEYWORD_EMPTY")
 * over its English message, then the client's translated message, then the
 * caller's fallback.
 */
export function apiErrorMessage(e: unknown, fallback: string): string {
  const code = (e as { response?: { data?: { code?: unknown } } })?.response?.data?.code
  if (typeof code === 'string' && /^[A-Z_]+$/.test(code)) {
    const key = `settings.Blocks.errors.${code}`
    if (i18n.global.te(key) || i18n.global.te(key, 'en')) return t(key)
  }
  return e instanceof Error && e.message ? e.message : fallback
}
