import { describe, it, expect, vi } from 'vitest'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { invalidateSessionData } from '@/shared/api/sessionScope'
import { applyStatusUpdate, editedStatuses, deletedStatusIds, markStatusEdited, markStatusDeleted, useStatusActions } from '../useStatusActions'
import { postApi } from '@/domains/post/infrastructure/postApi'
vi.mock('@/domains/post/infrastructure/postApi', () => ({ postApi: { favourite: vi.fn() } }))
const status = (id: string) => ({ id, favourited: false } as MastodonStatus)
describe('status copies and account isolation', () => {
  it('updates the original and every reblog copy without merging their timeline identities', () => {
    const list = [status('1'), { id: '20', reblog: status('1') }, { id: '30', reblog: status('1') }] as MastodonStatus[]
    applyStatusUpdate(list, '1', { favourited: true })
    expect(list.map(s => (s.reblog || s).favourited)).toEqual([true, true, true])
    expect(list.map(s => s.id)).toEqual(['1', '20', '30'])
  })
  it('clears full-status caches and rejects a late action result from a previous account', async () => {
    let resolve!: (s: MastodonStatus) => void
    vi.mocked(postApi.favourite).mockImplementationOnce(() => new Promise(r => { resolve = r }))
    const update = vi.fn()
    markStatusEdited(status('private'))
    markStatusEdited(status('1'))
    markStatusDeleted('old')
    const request = useStatusActions(update).toggleFavourite(status('1'))
    expect(update).toHaveBeenCalledTimes(1)
    expect(editedStatuses.get('1')?.favourited).toBe(true)
    invalidateSessionData()
    expect(editedStatuses.size).toBe(0)
    expect(deletedStatusIds.size).toBe(0)
    resolve({ ...status('1'), favourited: true })
    await request
    expect(update).toHaveBeenCalledTimes(1)
  })
})
