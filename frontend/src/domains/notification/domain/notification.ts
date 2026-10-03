import type { MastodonAccount, MastodonStatus, NotificationType } from '@/shared/types/mastodon'

/** In-app route a notification should open: the status if any, else the actor's profile. */
export function notificationTarget(n: { type: string; account: MastodonAccount; status?: MastodonStatus | null }): string {
  const s = n.status
  if (s && n.type !== 'follow' && n.type !== 'follow_request') {
    return `/@${s.account.acct || s.account.username}/status/${s.id}`
  }
  return `/@${n.account.acct || n.account.username}`
}

export function notificationText(type: NotificationType | string): string {
  switch (type) {
    case 'emoji_reaction': return '用表情回应了你的帖子'
    case 'favourite': return '喜欢了你的帖子'
    case 'reblog': return '转发了你的帖子'
    case 'follow': return '关注了你'
    case 'follow_request': return '请求关注你'
    case 'mention': return '提到了你'
    case 'poll': return '参与的投票已结束'
    case 'update': return '编辑了一条帖子'
    case 'status': return '发布了新帖子'
    default: return '与你互动'
  }
}

export type NotificationFilter = 'all' | 'mention'

export function notificationQuery(filter: NotificationFilter): { 'types[]'?: string[] } {
  return filter === 'mention' ? { 'types[]': ['mention'] } : {}
}
