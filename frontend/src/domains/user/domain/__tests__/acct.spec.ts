import { describe, it, expect } from 'vitest'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { pickAccountByAcct, normalizeAcct } from '../acct'

const acc = (id: string, username: string, acct: string) => ({ id, username, acct }) as MastodonAccount

describe('normalizeAcct', () => {
  it('strips a leading @ and lower-cases', () => {
    expect(normalizeAcct(' @Alice@Example.com ')).toBe('alice@example.com')
  })
})

describe('pickAccountByAcct', () => {
  const results = [acc('1', 'al', 'al@example.com'), acc('2', 'alice', 'alice@example.com'), acc('3', 'alice', 'alice@remote.social')]

  it('does not just take the first prefix-search hit', () => {
    expect(pickAccountByAcct(results, 'alice')?.id).toBe('2')
  })

  it('matches an exact acct including the domain', () => {
    expect(pickAccountByAcct(results, 'alice@remote.social')?.id).toBe('3')
    expect(pickAccountByAcct(results, '@ALICE@example.com')?.id).toBe('2')
  })

  it('maps user@local-domain to a Mastodon-style local acct', () => {
    const local = [acc('7', 'bob', 'bob')]
    expect(pickAccountByAcct(local, 'bob@my.host', 'my.host')?.id).toBe('7')
    expect(pickAccountByAcct(local, 'bob@elsewhere.org', 'my.host')).toBeUndefined()
  })

  it('returns undefined when nothing matches', () => {
    expect(pickAccountByAcct(results, 'carol')).toBeUndefined()
    expect(pickAccountByAcct(results, '')).toBeUndefined()
  })
})
