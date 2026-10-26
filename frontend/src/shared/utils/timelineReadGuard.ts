/** Keep a timeline removal newer than an HTTP snapshot from being undone by
 * that response. Track only active reads: a later fresh request may confirm
 * membership again (a stream delete can also mean an unfollow, not deletion). */
export function createTimelineReadGuard() {
  const reads = new Set<Set<string>>()
  return {
    remove(id: string) { for (const removed of reads) removed.add(id) },
    begin() {
      const removed = new Set<string>()
      reads.add(removed)
      return {
        wasRemoved: (id: string) => removed.has(id),
        filter<T extends { id: string }>(rows: T[]): T[] {
          return rows.filter(row => !removed.has(row.id) && !removed.has((row as { reblog?: { id: string } | null }).reblog?.id ?? ''))
        },
        close() { reads.delete(removed) },
      }
    },
  }
}
