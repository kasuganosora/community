import { describe, it, expect } from 'vitest'
import { conversationMediaLabel, conversationPreview, conversationTitle, conversationTarget } from '../conversation'
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

describe('conversationPreview', () => {
  it('flattens html to one line and prefers a content warning', () => {
    const html = '<p>line1<br>line2</p>'
    expect(conversationPreview(conv({ last_status: { id: '9', account: bob, content: html } as MastodonStatus }))).toBe('line1 line2')
    expect(conversationPreview(conv({
      last_status: { id: '9', account: bob, content: html, spoiler_text: '剧透' } as MastodonStatus,
    }))).toBe('内容警告：剧透')
  })
  it('uses a placeholder when there is nothing to show', () => {
    expect(conversationPreview(conv({}))).toBe('（没有可显示的消息）')
    expect(conversationPreview(conv({ last_status: { id: '9', account: bob, content: '<p>  </p>' } as MastodonStatus }))).toBe('（没有可显示的消息）')
  })
})

describe('conversationTarget', () => {
  it('opens the last status thread', () => {
    expect(conversationTarget(conv({ last_status: { id: '9', account: bob } as MastodonStatus }))).toBe('/@bob/status/9')
    expect(conversationTarget(conv({}))).toBeNull()
  })
})

describe('conversationMediaLabel', () => {
  it('counts images separately from other attachments', () => {
    const media = [{ id: 'a', type: 'image' }, { id: 'b', type: 'image' }, { id: 'c', type: 'video' }]
    expect(conversationMediaLabel(conv({ last_status: { id: '9', account: bob, media_attachments: media } as MastodonStatus }))).toBe('2 张图片，另有 1 个附件')
    expect(conversationMediaLabel(conv({}))).toBeNull()
  })
})
