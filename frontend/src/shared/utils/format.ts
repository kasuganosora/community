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

export function formatRelativeTime(dateStr: string, now: number = Date.now()): string {
  const date = new Date(dateStr).getTime()
  if (isNaN(date)) return ''
  // Guard against an invalid `now` (e.g. accidentally passed NaN); fall back to
  // the current time so the function always produces a meaningful result.
  const effectiveNow = Number.isFinite(now) ? now : Date.now()
  const diff = effectiveNow - date
  if (diff < 0) return 'now'

  const seconds = Math.floor(diff / 1000)
  const minutes = Math.floor(seconds / 60)
  const hours = Math.floor(minutes / 60)
  const days = Math.floor(hours / 24)

  if (seconds < 60) return `${seconds}s`
  if (minutes < 60) return `${minutes}m`
  if (hours < 24) return `${hours}h`
  if (days < 7) return `${days}d`

  const d = new Date(dateStr)
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
  if (seconds < 60) return `${seconds}s`
  const minutes = Math.floor(seconds / 60)
  if (minutes < 60) return `${minutes}m`
  const hours = Math.floor(minutes / 60)
  if (hours < 24) return `${hours}h`
  return `${Math.floor(hours / 24)}d`
}
