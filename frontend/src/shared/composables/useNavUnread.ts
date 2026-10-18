import { ref } from 'vue'
import client from '@/shared/api/client'
import { useNotificationUnread } from './useNotificationUnread'

/** Pages whose own badge is cleared while they are open. */
export type NavUnreadKind = 'notifications' | 'announcements' | 'conversations'

const announcements = ref(0)
const conversations = ref(0)
const suppressed = ref<NavUnreadKind | null>(null)
let generation = 0

interface UnreadCounts { notifications?: number, announcements?: number, conversations?: number }

const count = (n: unknown) => typeof n === 'number' && Number.isFinite(n) && n > 0 ? Math.floor(n) : 0

/**
 * One request (GET /api/v1/unread_counts) for all three nav dots. Called on
 * stream connect/reconnect, stream hints and window focus; never on a timer.
 */
async function refreshNavUnread(): Promise<boolean> {
  const mine = ++generation
  const { unread } = useNotificationUnread()
  try {
    const { data } = await client.get<UnreadCounts>('/api/v1/unread_counts')
    if (mine !== generation) return true
    unread.value = suppressed.value === 'notifications' ? 0 : count(data?.notifications)
    announcements.value = suppressed.value === 'announcements' ? 0 : count(data?.announcements)
    conversations.value = suppressed.value === 'conversations' ? 0 : count(data?.conversations)
    return true
  } catch {
    return false
  }
}

/** The page for `kind` is open (or null when none is): its dot is cleared. */
function setOpenPage(kind: NavUnreadKind | null) {
  suppressed.value = kind
  if (!kind) return
  ++generation
  if (kind === 'notifications') useNotificationUnread().unread.value = 0
  else if (kind === 'announcements') announcements.value = 0
  else conversations.value = 0
}

function resetNavUnread() {
  ++generation
  announcements.value = 0
  conversations.value = 0
  useNotificationUnread().resetUnread()
}

export function useNavUnread() {
  return { announcements, conversations, refreshNavUnread, setOpenPage, resetNavUnread }
}
