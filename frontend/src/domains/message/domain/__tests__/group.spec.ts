import { describe, expect, it } from 'vitest'
import { groupCanSend, mergeGroupMessages, safeGroupURL, type Group, type GroupMessage } from '../group'
const group = { id: 'one', membership: 'join', encryption: 'unencrypted', capabilities: { send_message: true } } as Group
const message = (id: string, created_at: number, body = id): GroupMessage => ({ id, created_at, body, sender: { id: 'a', display_name: 'A' }, kind: 'text', own: false, edited: false, redacted: false, reactions: [] })
describe('group lifecycle and timeline domain', () => {
  it('only permits plaintext sends for joined, verified, writable, current rooms', () => {
    expect(groupCanSend(group)).toBe(true)
    for (const override of [{ membership: 'invite' }, { membership: 'leave' }, { encryption: 'unknown' }, { encryption: 'encrypted' }, { replacement_id: 'new' }, { capabilities: { send_message: false } }]) expect(groupCanSend({ ...group, ...override } as Group)).toBe(false)
  })
  it('upserts old edited/redacted events without reordering remote timestamps', () => {
    const first = message('first', 999), second = message('second', 1)
    expect(mergeGroupMessages([first, second], [{ ...first, edited: true, body: 'edited' }, message('third', 0)]).map(m => [m.id, m.body])).toEqual([['first', 'edited'], ['second', 'second'], ['third', 'third']])
  })
  it('prepends only unseen historical entries and replaces duplicates', () => {
    const current = [message('second', 2), message('third', 3)]
    expect(mergeGroupMessages(current, [message('first', 1), message('second', 2, 'edit')], true).map(m => m.body)).toEqual(['first', 'edit', 'third'])
  })
  it('rejects executable or credential-bearing client URLs', () => {
    expect(safeGroupURL('javascript:alert(1)')).toBe('')
    expect(safeGroupURL('https://token@matrix.to')).toBe('')
    expect(safeGroupURL('https://matrix.to/#/room')).toBe('https://matrix.to/#/room')
  })
})
