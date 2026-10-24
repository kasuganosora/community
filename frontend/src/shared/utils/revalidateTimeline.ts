import axios from 'axios'
import { editedStatuses } from '@/domains/post/presentation/composables/useStatusActions'
import { mergeById } from './pagination'
import { TIMELINE_PAGE_SIZE } from './constants'

async function readTimelinePage<T>(
  fetchPage: (maxId: string | undefined, limit: number) => Promise<T[]>,
  cursor: string | undefined,
  limit: number,
) {
  try { return await fetchPage(cursor, limit) }
  catch (error) {
    // A deleted list or revoked scope must not leave its last private cards on screen.
    if (axios.isAxiosError(error) && [401, 403, 404].includes(error.response?.status ?? 0)) return [] as T[]
    throw error
  }
}

/** Snapshot before the request so a mutation that lands while it is in flight is kept. */
function captureEdits() {
  return new Map([...editedStatuses].map(([id, status]) => [id, { original: status, snapshot: { ...status } }]))
}

/** Drop optimistic edits once HTTP has returned the same row unchanged. */
function settleAuthoritativeEdits<T extends { id: string }>(rows: T[], edits: ReturnType<typeof captureEdits>) {
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

function embeddedStatusID(value: unknown): string | undefined {
  if (!value || typeof value !== 'object' || !('id' in value)) return
  const id = (value as { id?: unknown }).id
  return typeof id === 'string' && id !== '' ? id : undefined
}

/** Ids already on screen: the card, and the boost, reply parent, or quote it renders. */
export function representedStatusIDs<T extends { id: string }>(rows: readonly T[]): Set<string> {
  const seen = new Set<string>()
  for (const row of rows) {
    seen.add(row.id)
    const extra = row as { reblog?: unknown, reply?: unknown, quote?: unknown }
    for (const id of [embeddedStatusID(extra.reblog), embeddedStatusID(extra.reply), embeddedStatusID(extra.quote)]) {
      if (id) seen.add(id)
    }
  }
  return seen
}

export interface TimelineHead<T> {
  /** Loaded rows, with any that also appear in the fresh head replaced. Nothing is removed. */
  statuses: T[]
  newer: { id: string }[]
  head: T[]
}

/**
 * One head read. New ids wait on the pill. Rows already on screen are refreshed
 * when they appear in that page. The loaded tail is not walked and not deleted.
 */
export async function refreshTimelineHead<T extends { id: string }>(
  current: T[],
  fetchPage: (maxId: string | undefined, limit: number) => Promise<T[]>,
  valid: () => boolean,
  knownHead?: readonly string[],
): Promise<TimelineHead<T> | null> {
  const edits = captureEdits()
  const head = await readTimelinePage(fetchPage, undefined, TIMELINE_PAGE_SIZE)
  if (!valid()) return null
  settleAuthoritativeEdits(head, edits)
  if (!current.length) return { statuses: head, newer: [], head }
  const seen = representedStatusIDs(current)
  const known = new Set(knownHead ?? [])
  const newer: { id: string }[] = []
  for (const status of head) {
    if (!seen.has(status.id) && !known.has(status.id) && newer.length < TIMELINE_PAGE_SIZE) newer.push({ id: status.id })
  }
  const fresh = new Map(head.map(status => [status.id, status]))
  return { statuses: current.map(status => fresh.get(status.id) ?? status), newer, head }
}

/** Re-read a bounded historical window, not just since_id (which misses revocations
 * and delayed commits). Previously unseen rows wait behind the new-content button. */
export async function revalidateTimeline<T extends { id: string }>(
  current: T[],
  fetchPage: (maxId: string | undefined, limit: number) => Promise<T[]>,
  valid: () => boolean,
  knownHead?: readonly string[],
) {
  async function read(cursor: string | undefined, limit: number) {
    return readTimelinePage(fetchPage, cursor, limit)
  }
  const edits = captureEdits()
  const head = await read(undefined, TIMELINE_PAGE_SIZE)
  if (!valid()) return null
  if (!current.length) { settleAuthoritativeEdits(head, edits); return { statuses: head, newer: [] as T[] } }
  const first = current[0]!.id
  const last = current[current.length - 1]!.id
  const shown = new Set(current.map(status => status.id))
  const seen = representedStatusIDs(current)
  // IDs stay strings throughout; never round a snowflake through Number.
  if (!/^\d+$/.test(first) || !/^\d+$/.test(last)) return { statuses: head.filter(s => shown.has(s.id)), newer: head.filter(s => !seen.has(s.id)) }
  const pending = new Map<string, { id: string }>()
  const addPending = (id: string) => { if (!seen.has(id) && pending.size < TIMELINE_PAGE_SIZE) pending.set(id, { id }) }
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
      if (shown.has(status.id)) rows.push(status)
      else addPending(status.id)
    }
    const next = data.at(-1)?.id
    if (!next || data.length < TIMELINE_PAGE_SIZE || BigInt(next) <= BigInt(last)) break
    if (BigInt(next) >= BigInt(cursor)) throw new Error('timeline cursor did not advance')
    while (index < current.length && BigInt(current[index]!.id) >= BigInt(next)) index++
  }
  const verified = mergeById([], rows)
  settleAuthoritativeEdits([...head, ...verified], edits)
  return { statuses: verified, newer: [...pending.values()] }
}
