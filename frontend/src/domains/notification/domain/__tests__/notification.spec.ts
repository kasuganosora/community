import { describe, it, expect } from 'vitest'
import { notificationTarget, notificationText, notificationQuery } from '../notification'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'

const bob = { id: '2', username: 'bob', acct: 'bob' } as MastodonAccount
const alice = { id: '1', username: 'alice', acct: 'alice' } as MastodonAccount
const status = { id: '9', account: alice } as MastodonStatus

describe('notificationTarget', () => {
  it('opens the status for likes, reposts and mentions', () => {
    expect(notificationTarget({ type: 'favourite', account: bob, status })).toBe('/@alice/status/9')
    expect(notificationTarget({ type: 'emoji_reaction', account: bob, status })).toBe('/@alice/status/9')
    expect(notificationTarget({ type: 'mention', account: bob, status })).toBe('/@alice/status/9')
  })
  it('opens the actor profile for follows', () => {
    expect(notificationTarget({ type: 'follow', account: bob, status: null })).toBe('/@bob')
  })
})

describe('notificationText', () => {
  it('has Chinese text for every type', () => {
    expect(notificationText('favourite')).toBe('喜欢了你的帖子')
    expect(notificationText('emoji_reaction')).toBe('用表情回应了你的帖子')
    expect(notificationText('whatever')).toBe('与你互动')
  })
})

describe('notificationQuery', () => {
  it('filters mentions via types[]', () => {
    expect(notificationQuery('mention')).toEqual({ 'types[]': ['mention'] })
    expect(notificationQuery('all')).toEqual({})
  })
})
