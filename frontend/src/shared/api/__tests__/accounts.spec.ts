import { beforeEach, describe, expect, it } from 'vitest'
import { ACCOUNTS_KEY, findByToken, getAccount, listAccounts, markNeedsRelogin, nextUsableAccount, readAccounts, removeAccount, upsertAccount } from '../accounts'

const alice = { id: '1', username: 'alice', display_name: 'Alice', avatar: 'a.png' }
const bob = { id: '2', username: 'bob', display_name: 'Bob', avatar: 'b.png' }

describe('account registry', () => {
  beforeEach(() => localStorage.clear())

  it('stores accounts keyed by id in insertion order', () => {
    upsertAccount(bob, 'tb', 200)
    upsertAccount(alice, 'ta', 100)
    expect(listAccounts().map(a => a.username)).toEqual(['alice', 'bob'])
    expect(Object.keys(JSON.parse(localStorage.getItem(ACCOUNTS_KEY)!)).sort()).toEqual(['1', '2'])
  })

  it('refreshes profile and token but keeps addedAt', () => {
    upsertAccount(alice, 't1', 100)
    upsertAccount({ ...alice, display_name: 'A2' }, 't2', 999)
    expect(getAccount('1')).toMatchObject({ display_name: 'A2', token: 't2', addedAt: 100, needsRelogin: false })
  })

  it('drops another entry holding the same token', () => {
    upsertAccount(alice, 'same')
    upsertAccount(bob, 'same')
    expect(listAccounts().map(a => a.id)).toEqual(['2'])
  })

  it('removes one account only', () => {
    upsertAccount(alice, 'ta'); upsertAccount(bob, 'tb')
    expect(removeAccount('1')?.username).toBe('alice')
    expect(listAccounts().map(a => a.id)).toEqual(['2'])
    expect(removeAccount('nope')).toBeNull()
  })

  it('marks a rejected token as needing re-login and skips it for fallback', () => {
    upsertAccount(alice, 'ta', 1); upsertAccount(bob, 'tb', 2)
    expect(markNeedsRelogin('ta')?.id).toBe('1')
    expect(getAccount('1')).toMatchObject({ token: '', needsRelogin: true })
    expect(findByToken('ta')).toBeNull()
    expect(nextUsableAccount('2')).toBeNull()
    expect(nextUsableAccount(null)?.id).toBe('2')
    expect(markNeedsRelogin('')).toBeNull()
  })

  it('re-login clears the flag', () => {
    upsertAccount(alice, 'ta'); markNeedsRelogin('ta')
    upsertAccount(alice, 'ta2')
    expect(getAccount('1')).toMatchObject({ token: 'ta2', needsRelogin: false })
  })

  it('ignores corrupt or mismatched storage', () => {
    localStorage.setItem(ACCOUNTS_KEY, '{bad')
    expect(readAccounts()).toEqual({})
    localStorage.setItem(ACCOUNTS_KEY, JSON.stringify({ x: { id: 'y', username: 'u', token: 't' }, 3: { id: '3', username: 'c', token: 't3', addedAt: 1 } }))
    expect(Object.keys(readAccounts())).toEqual(['3'])
  })
})
