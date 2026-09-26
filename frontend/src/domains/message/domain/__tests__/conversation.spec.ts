import { describe, it, expect } from 'vitest'
import { conversationTitle, conversationTarget } from '../conversation'
import type { MastodonAccount, MastodonConversation, MastodonStatus } from '@/shared/types/mastodon'

const acc = (id: string, name: string) => ({ id, username: name.toLowerCase(), acct: name.toLowerCase(), display_name: name }) as MastodonAccount
const me = acc('1', 'Me')
const bob = acc('2', 'Bob')
const conv = (over: Partial<MastodonConversation>): MastodonConversation => ({ id: 'c', unread: false, accounts: [], last_status: null, ...over })

describe('conversationTitle', () => {
  it('lists the other participants', () => {
    expect(conversationTitle(conv({ accounts: [me, bob, acc('3', 'Carol')] }), '1')).toBe('Bob、Carol')
  })
  it('falls back to the last status author, then a placeholder', () => {
    expect(conversationTitle(conv({ last_status: { id: '9', account: bob } as MastodonStatus }), '1')).toBe('Bob')
    expect(conversationTitle(conv({}), '1')).toBe('未知会话')
  })
})

describe('conversationTarget', () => {
  it('opens the last status thread', () => {
    expect(conversationTarget(conv({ last_status: { id: '9', account: bob } as MastodonStatus }))).toBe('/@bob/status/9')
    expect(conversationTarget(conv({}))).toBeNull()
  })
})
