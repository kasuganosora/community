import { describe, it, expect } from 'vitest'
import { followStateOf, followButtonLabel } from '../relationship'
import type { MastodonRelationship } from '@/shared/types/mastodon'

const rel = (p: Partial<MastodonRelationship>) => ({ id: '1', following: false, requested: false, ...p }) as MastodonRelationship

describe('followStateOf', () => {
  it('is unknown until loaded', () => expect(followStateOf(undefined)).toBe('unknown'))
  it('maps following / requested / none', () => {
    expect(followStateOf(rel({ following: true }))).toBe('following')
    expect(followStateOf(rel({ requested: true }))).toBe('requested')
    expect(followStateOf(rel({}))).toBe('none')
  })
})

describe('followButtonLabel', () => {
  it('offers follow-back when followed by them', () => {
    expect(followButtonLabel('none', true)).toBe('回关')
    expect(followButtonLabel('none')).toBe('关注')
    expect(followButtonLabel('following')).toBe('已关注')
  })
})
