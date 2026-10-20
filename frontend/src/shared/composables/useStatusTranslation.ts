import { ref } from 'vue'
import { isAxiosError } from 'axios'
import client from '@/shared/api/client'
import { i18n, t } from '@/shared/i18n'

/** Mastodon Translation entity. */
export interface StatusTranslation {
  content: string
  spoiler_text: string
  poll?: { id: string; options: { title: string }[] } | null
  media_attachments: { id: string; description: string }[]
  detected_source_language: string
  language?: string
  provider: string
}

const enabled = ref(false)
let pending: Promise<void> | null = null

/** Whether the instance offers translation (configuration.translation.enabled), loaded once per page. */
export function useTranslationEnabled() {
  if (!pending) {
    pending = client.get<{ configuration?: { translation?: { enabled?: boolean } } }>('/api/v2/instance')
      .then(({ data }) => { enabled.value = data?.configuration?.translation?.enabled === true })
      .catch(() => { pending = null })
  }
  return enabled
}

/** Current UI locale (zh-CN, zh-TW, ja, en, ru, fr, de). */
export function uiLocale(): string {
  const l = i18n.global.locale as unknown as { value?: string } | string
  return typeof l === 'string' ? l : (l.value || 'en')
}

function primary(lang: string): string {
  return lang.toLowerCase().replace('_', '-').split('-')[0] || ''
}

/** A post is offered for translation when its language differs from the UI language. */
export function needsTranslation(language: string | null | undefined, target = uiLocale()): boolean {
  if (!language) return false
  const a = primary(language), b = primary(target)
  if (!a || a === 'und') return false
  return a !== b
}

/** Localised name of a language code in the UI language. */
export function languageName(code: string | null | undefined): string {
  if (!code) return t('shared.Translation.unknownLanguage')
  try {
    const names = new Intl.DisplayNames([uiLocale()], { type: 'language' })
    return names.of(code) || code
  } catch {
    return code
  }
}

const ERROR_KEYS: Record<string, string> = {
  translation_disabled: 'translationDisabled',
  translation_login_required: 'translationLoginRequired',
  translation_rate_limited: 'translationRateLimited',
  translation_user_quota_exceeded: 'translationUserQuota',
  translation_daily_quota_exceeded: 'translationDailyQuota',
  translation_unavailable: 'translationUnavailable',
  translation_failed: 'translationFailed',
  translation_same_language: 'translationSameLanguage',
  translation_nothing_to_translate: 'translationNothing',
  translation_invalid_language: 'translationFailed',
}

/** Localised message for a failed translation request. */
export function translationErrorMessage(cause: unknown): string {
  if (isAxiosError(cause)) {
    const code = (cause.response?.data as { error_code?: string } | undefined)?.error_code
    if (code && ERROR_KEYS[code]) return t(`apiError.${ERROR_KEYS[code]}`)
    if (cause.response?.status === 429) return t('apiError.translationRateLimited')
  }
  return t('apiError.translationFailed')
}

export async function translateStatus(id: string, lang = uiLocale()): Promise<StatusTranslation> {
  const { data } = await client.post<StatusTranslation>(`/api/v1/statuses/${encodeURIComponent(id)}/translate`, { lang }, { timeout: 90000 })
  return data
}
