import axios from 'axios'
import { editedStatuses } from '@/domains/post/presentation/composables/useStatusActions'
import { mergeById } from './pagination'
import { TIMELINE_PAGE_SIZE } from './constants'

/** Re-read a bounded historical window, not just since_id (which misses revocations
 * and delayed commits). Previously unseen rows wait behind the new-content button. */
export async function revalidateTimeline<T extends { id: string }>(
  current: T[],
  fetchPage: (maxId: string | undefined, limit: number) => Promise<T[]>,
  valid: () => boolean,
  knownHead?: readonly string[],
) {
  async function read(cursor: string | undefined, limit: number) {
    try { return await fetchPage(cursor, limit) }
    catch (error) {
      // A deleted list or revoked scope must not leave its last private cards on screen.
      if (axios.isAxiosError(error) && [401, 403, 404].includes(error.response?.status ?? 0)) return [] as T[]
      throw error
    }
  }
  const edits = new Map([...editedStatuses].map(([id, status]) => [id, { original: status, snapshot: { ...status } }]))
  function authoritative(rows: T[]) {
    for (const row of rows) {
      const reblog = (row as { reblog?: { id: string } | null }).reblog
      for (const id of [row.id, reblog?.id]) {
        if (!id) continue
        const captured = edits.get(id)
        const current = editedStatuses.get(id)
        if (captured && current === captured.original && Object.keys(current).length === Object.keys(captured.snapshot).length && Object.entries(captured.snapshot).every(([key, value]) => current[key as keyof typeof current] === value)) editedStatuses.delete(id)
      }
    }
  }
  const head = await read(undefined, TIMELINE_PAGE_SIZE)
  if (!valid()) return null
  if (!current.length) { authoritative(head); return { statuses: head, newer: [] as T[] } }
  const first = current[0]!.id
  const last = current[current.length - 1]!.id
  // IDs stay strings throughout; never round a snowflake through Number.
  if (!/^\d+$/.test(first) || !/^\d+$/.test(last)) return { statuses: head.filter(s => current.some(old => old.id === s.id)), newer: head.filter(s => !current.some(old => old.id === s.id)) }
  const old = new Set(current.map(status => status.id))
  const pending = new Map<string, { id: string }>()
  const addPending = (id: string) => { if (!old.has(id) && pending.size < TIMELINE_PAGE_SIZE) pending.set(id, { id }) }
  for (const status of head) {
    if (!knownHead?.includes(status.id)) addPending(status.id)
  }
  const rows: T[] = []
  let index = 0
  while (index < current.length) {
    // Target the next *old* ID, so arbitrary delayed inserts cannot push a
    // retained (possibly focused) card outside an artificial page limit.
    // Each request resolves at least one old ID; normal windows need one per page.
    const cursor = (BigInt(current[index]!.id) + 1n).toString()
    const data = await read(cursor, TIMELINE_PAGE_SIZE)
    if (!valid()) return null
    for (const status of data) {
      if (!/^\d+$/.test(status.id) || BigInt(status.id) < BigInt(last)) continue
      if (old.has(status.id)) rows.push(status)
      else addPending(status.id)
    }
    const next = data.at(-1)?.id
    if (!next || data.length < TIMELINE_PAGE_SIZE || BigInt(next) <= BigInt(last)) break
    if (BigInt(next) >= BigInt(cursor)) throw new Error('timeline cursor did not advance')
    while (index < current.length && BigInt(current[index]!.id) >= BigInt(next)) index++
  }
  const verified = mergeById([], rows)
  authoritative([...head, ...verified])
  return { statuses: verified, newer: [...pending.values()] }
}
