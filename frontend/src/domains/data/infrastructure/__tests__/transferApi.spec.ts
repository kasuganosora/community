import { afterEach, describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { transferApi, type TransferJob } from '../transferApi'
afterEach(() => vi.restoreAllMocks())
describe('transfer API scope and downloads', () => {
 it('sends personal and site packages to distinct authenticated endpoints with their selected format', async () => {
  const post = vi.spyOn(client, 'post').mockResolvedValue({ data: { id: 'job' } }); const file = new File(['data'], 'backup.zip')
  await transferApi.upload('personal', file, 'mastodon', 'following'); await transferApi.upload('site', file, 'misskey', 'archive')
  expect(post.mock.calls[0]![0]).toBe('/api/v1/data/imports'); expect(post.mock.calls[1]![0]).toBe('/api/v1/admin/data/imports')
  const data = post.mock.calls[0]![1] as FormData; expect(data.get('file')).toBe(file); expect(data.get('source')).toBe('mastodon'); expect(data.get('kind')).toBe('following')
 })
 it('executes exactly the reviewed revision and exports through the authenticated blob client', async () => {
  const post = vi.spyOn(client, 'post').mockResolvedValue({ data: {} }); const get = vi.spyOn(client, 'get').mockResolvedValue({ data: new Blob(['zip']) })
  await transferApi.execute('personal', { id: 'job/a', revision: 'version2' } as TransferJob); await transferApi.export()
  expect(post.mock.calls[0]!.slice(0, 2)).toEqual(['/api/v1/data/imports/job%2Fa/execute', { revision: 'version2' }]); expect(get).toHaveBeenCalledWith('/api/v1/data/export', expect.objectContaining({ responseType: 'blob', timeout: 300000 }))
 })
})
