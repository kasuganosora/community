import { useRouter } from 'vue-router'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { StoredAccount } from '@/shared/api/accounts'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { MessagePlugin } from '@/shared/ui/feedback'

/** Profile snapshot → the shape UserAvatar/EmojiText accept. */
export function asAvatarAccount(a: StoredAccount): MastodonAccount {
  return { id: a.id, username: a.username, acct: a.acct ?? a.username, display_name: a.display_name, avatar: a.avatar, avatar_static: a.avatar, emojis: [] } as unknown as MastodonAccount
}

export function useAccountActions(close: () => void = () => {}) {
  const router = useRouter()
  const auth = useAuthStore()

  function addAccount(username?: string) {
    close()
    router.push({ name: 'login', query: username ? { add: '1', username } : { add: '1' } }).catch(() => {})
  }

  function switchTo(a: StoredAccount) {
    const r = auth.switchAccount(a.id)
    if (r === 'relogin') {
      MessagePlugin.warning(`@${a.username} 的登录已失效，请重新登录`)
      addAccount(a.username)
    } else {
      close()
    }
  }

  async function signOut(a: StoredAccount) {
    try {
      const switched = await auth.removeAccount(a.id)
      if (!switched && a.id === auth.currentUser?.id) {
        close()
        router.push({ name: 'landing' }).catch(() => {})
      } else if (!switched) {
        MessagePlugin.success(`已退出 @${a.username}`)
      }
    } catch (e) {
      console.error('Logout failed:', e)
      MessagePlugin.error('退出失败，请稍后重试')
    }
  }

  return { addAccount, switchTo, signOut }
}
