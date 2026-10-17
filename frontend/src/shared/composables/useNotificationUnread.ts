import { computed, ref } from 'vue'
import client from '@/shared/api/client'

/** Badge cap: the count stops at this many ("40+"). */
export const UNREAD_CAP = 40

const unread = ref(0)
const lastReadId = ref<string | null>(null)
let markerLoaded = false
let generation = 0

function newer(a: string, b: string | null): boolean {
  if (!b) return true
  try { return BigInt(a) > BigInt(b) } catch { return a > b }
}

async function loadMarker() {
  if (markerLoaded) return
  const { data } = await client.get<Record<string, { last_read_id?: string }>>('/api/v1/markers', { params: { 'timelines[]': ['notifications'] } })
  const id = data?.notifications?.last_read_id
  if (id && newer(id, lastReadId.value)) lastReadId.value = id
  markerLoaded = true
}

/** Recount unread notifications (newer than the notifications marker). */
async function refreshUnread(): Promise<boolean> {
  const mine = ++generation
  try {
    await loadMarker()
    const params: Record<string, string | number> = { limit: UNREAD_CAP }
    if (lastReadId.value) params.since_id = lastReadId.value
    const { data } = await client.get<{ id: string }[]>('/api/v1/notifications', { params })
    if (mine !== generation) return true
    unread.value = (Array.isArray(data) ? data : []).filter(n => newer(n.id, lastReadId.value)).length
    return true
  } catch {
    return false
  }
}

/** The user has seen everything up to `id`: clear the badge and save the marker. */
async function markRead(id: string | undefined) {
  ++generation
  unread.value = 0
  if (!id || !newer(id, lastReadId.value)) return
  lastReadId.value = id
  try {
    await client.post('/api/v1/markers', { notifications: { last_read_id: id } })
  } catch { /* the badge recount will show it again if the save failed */ }
}

function resetUnread() {
  ++generation
  unread.value = 0
  lastReadId.value = null
  markerLoaded = false
}

export function useNotificationUnread() {
  const label = computed(() => unread.value >= UNREAD_CAP ? `${UNREAD_CAP}+` : String(unread.value))
  return { unread, label, refreshUnread, markRead, resetUnread }
}
