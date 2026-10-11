import { describe, expect, it } from 'vitest'
import { avatarIdentity, identiconPattern } from '../accountAvatar'

describe('account default avatar identity', () => {
  it('includes the canonical domain, preserves username case and ignores browsing aliases', () => {
    expect(avatarIdentity({ username: 'Alice', acct: 'Alice', url: 'https://canonical.test/@Alice', uri: '' })).toBe('Alice@canonical.test')
    expect(avatarIdentity({ username: 'alice', acct: 'alice@REMOTE.test', url: '', uri: '' })).toBe('alice@remote.test')
    expect(avatarIdentity({ username: 'alice', acct: 'alice', url: '', uri: 'https://canonical.test:8443/users/alice' })).toBe('alice@canonical.test:8443')
  })
  it('is stable, symmetric and separates same-name accounts on different domains', () => {
    const a = identiconPattern('alice@one.test')
    expect(identiconPattern('alice@one.test')).toEqual(a)
    expect(identiconPattern('alice@two.test')).not.toEqual(a)
    expect(identiconPattern('bob@one.test')).not.toEqual(a)
    for (const cell of a.cells) expect(a.cells).toContainEqual({ x: 108 - cell.x, y: cell.y })
  })
  it('matches the backend seed palette and bit geometry even when the image cannot be loaded', () => {
    const seed = '0001020100' + '00'.repeat(27)
    expect(identiconPattern('unused', `/api/v1/avatars/default/${seed}.png`)).toEqual({
      background: 'rgb(240,243,248)', foreground: 'rgb(48,49,50)', cells: [{ x: 14, y: 14 }, { x: 94, y: 14 }, { x: 54, y: 54 }],
    })
  })
})
