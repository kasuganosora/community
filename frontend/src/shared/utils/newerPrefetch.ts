interface Slot<T> {
  key: string
  promise: Promise<T | null>
  rows?: T
  settled: boolean
  failed: boolean
}

/**
 * One in-flight or finished prefetch, keyed by the set of unseen ids.
 * A new key replaces the slot; callers that are already waiting follow it
 * instead of starting another request.
 */
export function createNewerPrefetch<T>() {
  let slot: Slot<T> | undefined

  function schedule(key: string, load: () => Promise<T> | T) {
    if (!key) {
      slot = undefined
      return
    }
    if (slot && slot.key === key && !slot.failed) return
    const next: Slot<T> = { key, settled: false, failed: false, promise: Promise.resolve(null) }
    let pending: Promise<T>
    try {
      pending = Promise.resolve(load())
    } catch (error) {
      pending = Promise.reject(error)
    }
    next.promise = pending.then(rows => {
      if (slot !== next) return null
      next.rows = rows
      next.settled = true
      return rows
    }).catch(() => {
      if (slot === next) {
        next.failed = true
        next.settled = true
      }
      return null
    })
    slot = next
  }

  function ready(key: string): T | null {
    if (!slot || slot.key !== key || !slot.settled || slot.failed || slot.rows === undefined) return null
    return slot.rows
  }

  async function consume(): Promise<T | null> {
    const seen = new Set<Promise<T | null>>()
    for (;;) {
      const current = slot
      if (!current || current.failed) return null
      if (current.settled) return current.rows ?? null
      if (seen.has(current.promise)) return null
      seen.add(current.promise)
      await current.promise
    }
  }

  return {
    schedule,
    ready,
    consume,
    clear() { slot = undefined },
  }
}

/** Stable identity for the posts waiting behind the pill. */
export function newerPrefetchKey(ids: readonly { id: string }[]): string {
  return ids.map(item => item.id).slice().sort().join(',')
}
