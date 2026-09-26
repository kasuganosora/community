/** Append a page to a list, skipping items that are already present (by id). */
export function mergeById<T extends { id: string }>(existing: T[], page: T[]): T[] {
  const seen = new Set(existing.map(i => i.id))
  const fresh = page.filter(i => {
    if (seen.has(i.id)) return false
    seen.add(i.id)
    return true
  })
  return fresh.length ? [...existing, ...fresh] : existing
}

/** Whether another page may exist after receiving `received` items for a request of `pageSize`. */
export function hasMoreAfter(received: number, pageSize: number): boolean {
  return received >= pageSize
}
