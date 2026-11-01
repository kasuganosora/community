import type { MastodonAccount, MastodonStatus, NotificationType } from '@/shared/types/mastodon'

import { t } from '@/shared/i18n'
/** In-app route a notification should open: the status if any, else the actor's profile. */
export function notificationTarget(n: { type: string; account: MastodonAccount; status?: MastodonStatus | null }): string {
  const s = n.status
  if (s && n.type !== 'follow' && n.type !== 'follow_request') {
    return `/@${s.account.acct || s.account.username}/status/${s.id}`
  }
  return `/@${n.account.acct || n.account.username}`
}

/**
 * Type glyph for a notification row. Emoji reactions are not replies: the
 * speech-bubble icon is only for mentions. When a reaction emoji is present
 * the row shows that emoji in the type slot instead of this fallback.
 */
export function notificationIcon(type: NotificationType | string): string {
  switch (type) {
    case 'emoji_reaction': return 'happy'
    case 'favourite': return 'heart-filled'
    case 'reblog': return 'swap'
    case 'follow': return 'user-add'
    case 'follow_request': return 'user-add'
    case 'mention': return 'chat'
    case 'poll': return 'chart-bar'
    case 'update': return 'edit'
    default: return 'notification'
  }
}

/**
 * Status edits are not inbox items. Saves that only refresh counters or
 * reactions were stored as type "update" and shown as an edit.
 */
export function isInboxNotification(type: string): boolean {
  return type !== 'update' && type !== 'status_update'
}

/** ownPoll: the viewer created the poll ("your poll has ended"). */
export function notificationText(type: NotificationType | string, ownPoll = false): string {
  switch (type) {
    case 'emoji_reaction': return t('notification.notification.s1')
    case 'favourite': return t('notification.notification.s2')
    case 'reblog': return t('notification.notification.s3')
    case 'follow': return t('notification.notification.s4')
    case 'follow_request': return t('notification.notification.s5')
    case 'mention': return t('notification.notification.s6')
    case 'poll': return ownPoll ? t('notification.notification.pollOwn') : t('notification.notification.s7')
    case 'update': return t('notification.notification.s8')
    case 'status': return t('notification.notification.s9')
    default: return t('notification.notification.s10')
  }
}

export type NotificationFilter = 'all' | 'mention'

export function notificationQuery(filter: NotificationFilter): { 'types[]'?: string[] } {
  return filter === 'mention' ? { 'types[]': ['mention'] } : {}
}
