import { describe, expect, it } from 'vitest'
import { chatAvatars, chatPeerAcct, chatPreview, chatTitle, type ChatConversation, type ChatMember } from '../chat'
import type { MastodonAccount } from '@/shared/types/mastodon'

const acc = (id: string, name: string, acct = name): MastodonAccount => ({ id, username: name, acct, display_name: name.toUpperCase(), avatar: `/a/${id}.png` } as MastodonAccount)
const member = (id: string, name: string, extra: Partial<ChatMember> = {}): ChatMember => ({ id, kind: 'local', account_id: id, matrix_id: `@${name}:x`, account: acc(id, name), ...extra })
const chat = (members: ChatMember[], extra: Partial<ChatConversation> = {}): ChatConversation => ({ id: 'c', room_id: '!r', revision: 1, encrypted: false, title: '', creator_id: '1', can_manage: true, members, ...extra })
const me = member('1', 'sion', { own: true })

describe('chat display', () => {
  it('names a 1:1 chat after the other member, with their acct and avatar', () => {
    const c = chat([me, member('2', 'kanna', { account: acc('2', 'kanna', 'kanna@maid.lat') })])
    expect(chatTitle(c, '1')).toBe('KANNA')
    expect(chatPeerAcct(c, '1')).toBe('@kanna@maid.lat')
    expect(chatAvatars(c, '1').map(a => a.id)).toEqual(['2'])
  })

  it('keeps a room name and joins group names with +N', () => {
    expect(chatTitle(chat([me, member('2', 'a')], { title: 'Team' }), '1')).toBe('Team')
    const group = chat([me, member('2', 'a'), member('3', 'b'), member('4', 'c'), member('5', 'd'), member('6', 'e', { active: false })])
    expect(chatTitle(group, '1')).toMatch(/^A.*B.*C.*1/)
    expect(chatTitle(group, '1')).not.toContain('D')
    expect(chatPeerAcct(group, '1')).toBe('')
    expect(chatAvatars(group, '1')).toHaveLength(3)
  })

  it('falls back to the member fields when no account is known', () => {
    const c = chat([me, { id: '9', kind: 'matrix', account_id: '0', matrix_id: '@bob:m.org', display_name: 'Bob' }])
    expect(chatTitle(c, '1')).toBe('Bob')
    expect(chatAvatars(c, '1')).toEqual([])
  })

  it('previews the last message, encrypted ones without content', () => {
    expect(chatPreview(chat([me], { last_message: { event_id: 'e', created_at: 1, own: false, encrypted: false, body: 'hi\nthere', msgtype: 'm.text' } }))).toBe('hi there')
    expect(chatPreview(chat([me], { last_message: { event_id: 'e', created_at: 1, own: true, encrypted: false, body: 'yo', msgtype: 'm.text' } }))).toContain('yo')
    const enc = chatPreview(chat([me], { last_message: { event_id: 'e', created_at: 1, own: false, encrypted: true } }))
    expect(enc).not.toBe('')
    expect(enc).not.toContain('message.chat')
    expect(chatPreview(chat([me]))).toBe('')
  })
})
