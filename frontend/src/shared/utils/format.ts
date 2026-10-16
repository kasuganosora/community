// Debounce utility
export function debounce<Args extends unknown[]>(
  fn: (...args: Args) => unknown,
  delay: number
): ((...args: Args) => void) & { cancel: () => void } {
  let timeoutId: ReturnType<typeof setTimeout> | null = null
  const debounced = function (this: unknown, ...args: Args) {
    if (timeoutId) {
      clearTimeout(timeoutId)
    }
    timeoutId = setTimeout(() => fn.apply(this, args), delay)
  } as ((...args: Args) => void) & { cancel: () => void }
  debounced.cancel = () => {
    if (timeoutId) {
      clearTimeout(timeoutId)
      timeoutId = null
    }
  }
  return debounced
}

// Throttle utility
export function throttle<Args extends unknown[]>(
  fn: (...args: Args) => unknown,
  limit: number
): (...args: Args) => void {
  let inThrottle = false
  let lastArgs: Args | null = null
  return function (this: unknown, ...args: Args) {
    if (!inThrottle) {
      fn.apply(this, args)
      inThrottle = true
      setTimeout(() => {
        inThrottle = false
        if (lastArgs) {
          fn.apply(this, lastArgs)
          lastArgs = null
        }
      }, limit)
    } else {
      lastArgs = args
    }
  }
}

export function formatCount(count: number): string {
  if (!Number.isFinite(count) || count < 0) return '0'
  if (count >= 1_000_000) {
    return (count / 1_000_000).toFixed(1).replace(/\.0$/, '') + 'M'
  }
  if (count >= 1_000) {
    return (count / 1_000).toFixed(1).replace(/\.0$/, '') + 'K'
  }
  return count.toString()
}

let fmtLocale = 'en'
/** Locale used by the compact time formatters; set once by the i18n module. */
export function setFormatLocale(locale: string): void { fmtLocale = locale }

type TimeUnit = 'second' | 'minute' | 'hour' | 'day'
const SHORT: Record<TimeUnit, string> = { second: 's', minute: 'm', hour: 'h', day: 'd' }
function compact(n: number, unit: TimeUnit): string {
  if (fmtLocale === 'en') return `${n}${SHORT[unit]}`
  try {
    return new Intl.NumberFormat(fmtLocale, { style: 'unit', unit, unitDisplay: fmtLocale === 'ja' ? 'short' : 'narrow' }).format(n)
  } catch {
    return `${n}${SHORT[unit]}`
  }
}
function nowLabel(): string {
  if (fmtLocale === 'en') return 'now'
  try { return new Intl.RelativeTimeFormat(fmtLocale, { numeric: 'auto' }).format(0, 'second') } catch { return 'now' }
}

export function formatRelativeTime(dateStr: string, now: number = Date.now()): string {
  const date = new Date(dateStr).getTime()
  if (isNaN(date)) return ''
  // Guard against an invalid `now` (e.g. accidentally passed NaN); fall back to
  // the current time so the function always produces a meaningful result.
  const effectiveNow = Number.isFinite(now) ? now : Date.now()
  const diff = effectiveNow - date
  if (diff < 0) return nowLabel()

  const seconds = Math.floor(diff / 1000)
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)
  const days = Math.floor(hours / 24)

  if (seconds < 60) return compact(seconds, 'second')
  if (minutes < 60) return compact(minutes, 'minute')
  if (hours < 24) return compact(hours, 'hour')
  if (days < 7) return compact(days, 'day')

  const d = new Date(dateStr)
  if (fmtLocale !== 'en') {
    const sameYear = d.getFullYear() === new Date(effectiveNow).getFullYear()
    return d.toLocaleDateString(fmtLocale, { month: 'short', day: 'numeric', ...(sameYear ? {} : { year: 'numeric' as const }) })
  }
  const month = d.toLocaleString('en-US', { month: 'short' })
  const day = d.getDate()
  const year = d.getFullYear()
  const currentYear = new Date(effectiveNow).getFullYear()

  if (year === currentYear) return `${month} ${day}`
  return `${month} ${day}, ${year}`
}

/** Compact time remaining until `dateStr` ("45s", "12m", "5h", "3d"); "0s" once passed. */
export function formatTimeLeft(dateStr: string, now: number = Date.now()): string {
  const date = new Date(dateStr).getTime()
  if (isNaN(date)) return ''
  const effectiveNow = Number.isFinite(now) ? now : Date.now()
  const seconds = Math.max(0, Math.floor((date - effectiveNow) / 1000))
  if (seconds < 60) return compact(seconds, 'second')
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return compact(minutes, 'minute')
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return compact(hours, 'hour')
  return compact(Math.floor(hours / 24), 'day')
}

/**
 * Whether a status was edited after it was posted. Older servers (and ours
 * before edit tracking) echo updated_at as edited_at, so a value at or before
 * created_at is not an edit.
 */
export function wasEdited(status: { created_at: string; edited_at?: string | null }): boolean {
  if (!status.edited_at) return false
  const edited = Date.parse(status.edited_at)
  const created = Date.parse(status.created_at)
  if (Number.isNaN(edited)) return false
  if (Number.isNaN(created)) return true
  return Math.floor(edited / 1000) > Math.floor(created / 1000)
}
