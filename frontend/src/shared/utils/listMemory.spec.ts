import { describe, expect, it } from 'vitest'
import { readListSnapshot, saveListSnapshot } from './listMemory'
import { invalidateSessionData } from '@/shared/api/sessionScope'

describe('bounded account-scoped route memory', () => {
  it('keeps at most eight pages of bodies across routes and clears private snapshots at logout', () => {
    invalidateSessionData()
    const rows = Array.from({ length: 160 }, (_, id) => ({ id, content: 'private' }))
    saveListSnapshot('first', rows, true)
    saveListSnapshot('second', rows, true)
    expect(readListSnapshot('first')).toBeUndefined()
    expect(readListSnapshot('second')?.items).toHaveLength(160)
    invalidateSessionData()
    expect(readListSnapshot('second')).toBeUndefined()
  })
  it('retains only eight scroll-only routes and updates recency on access', () => {
    invalidateSessionData()
    for (let i = 0; i < 8; i++) saveListSnapshot(String(i), [], true)
    readListSnapshot('0'); saveListSnapshot('8', [], true)
    expect(readListSnapshot('1')).toBeUndefined()
    expect(readListSnapshot('0')).toBeDefined()
  })
})
