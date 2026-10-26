import { effectScope } from 'vue'
import { expect, it, vi } from 'vitest'
import { useRelationships } from '../useRelationships'
import { userApi } from '../../../infrastructure/userApi'
import { sessionEpoch } from '@/shared/api/sessionScope'
import type { MastodonRelationship } from '@/shared/types/mastodon'

it('clears account-specific follow states and ignores old hydration and mutation results', async () => {
  const scope = effectScope()
  const relationships = scope.run(() => useRelationships())!
  const rel = { id: 'target', following: false, requested: false } as MastodonRelationship
  relationships.rels.set(rel.id, rel)
  let finishRead!: (value: MastodonRelationship[]) => void
  let finishFollow!: (value: MastodonRelationship) => void
  vi.spyOn(userApi, 'getRelationships').mockImplementation(() => new Promise(resolve => { finishRead = resolve }))
  vi.spyOn(userApi, 'follow').mockImplementation(() => new Promise(resolve => { finishFollow = resolve }))
  const read = relationships.load(['other'])
  const follow = relationships.toggleFollow(rel.id)
  sessionEpoch.value++
  expect(relationships.rels.size).toBe(0)
  expect(relationships.pending.size).toBe(0)
  relationships.pending.add(rel.id)
  finishRead([{ ...rel, id: 'other' }]); finishFollow({ ...rel, following: true })
  await read; await follow
  expect(relationships.rels.size).toBe(0)
  expect(relationships.pending.has(rel.id)).toBe(true)
  scope.stop()
})
