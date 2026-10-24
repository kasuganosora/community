import { t } from '@/shared/i18n'

/**
 * Interactive reads (timeline pages, unread badges) must leave the loading UI
 * even when the browser keeps the socket Pending. A lossy VPN is often
 * reported as a slow link, which would otherwise stretch the shared axios
 * budget to 45s and hold 「加载中」 for that whole time.
 */
export const INTERACTIVE_READ_TIMEOUT_MS = 20_000

export async function settleWithin<T>(run: (signal: AbortSignal) => Promise<T>, ms = INTERACTIVE_READ_TIMEOUT_MS): Promise<T> {
  const controller = new AbortController()
  let timer: ReturnType<typeof setTimeout> | undefined
  const request = run(controller.signal).then(
    value => ({ ok: true as const, value }),
    error => ({ ok: false as const, error }),
  )
  const timeout = new Promise<{ ok: false, error: Error }>(resolve => {
    timer = setTimeout(() => {
      controller.abort()
      resolve({ ok: false, error: Object.assign(new Error(t('shared.client.s1')), { code: 'ETIMEDOUT' }) })
    }, ms)
  })
  try {
    const result = await Promise.race([request, timeout])
    if (result.ok) return result.value
    throw result.error
  } finally {
    clearTimeout(timer)
  }
}
