import { afterEach, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { discoveryApi } from '../discoveryApi'
import { suggestionApi } from '@/domains/user/infrastructure/suggestionApi'
afterEach(() => vi.restoreAllMocks())

it('requests ranked discovery and rolling topics on their dedicated routes; suggestions use v2', async () => {
  const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [] })
  await discoveryApi.statuses('opaque cursor')
  await discoveryApi.tags({ window: '24h', scope: 'federated', limit: 5 })
  await suggestionApi.list({ limit: 3 })
  expect(get.mock.calls[0]).toEqual(['/api/v1/discovery/statuses', { params: { limit: 20, cursor: 'opaque cursor' }, signal: undefined }])
  expect(get.mock.calls[1]?.[0]).toBe('/api/v1/discovery/tags')
  expect(get.mock.calls[2]?.[0]).toBe('/api/v2/suggestions')
})
