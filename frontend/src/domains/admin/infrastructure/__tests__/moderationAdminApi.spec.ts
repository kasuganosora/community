import { afterEach, describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { moderationAdminApi as api } from '../moderationAdminApi'
afterEach(() => vi.restoreAllMocks())
describe('moderation API contracts', () => {
  it('omits blank account filters, preserves offset zero and reads total header', async () => {
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [], headers: { 'x-total-count': '84' } })
    expect(await api.accounts({ username: '', display_name: '', status: '', origin: 'local', offset: 0, limit: 40 })).toEqual({ items: [], total: 84 })
    expect(get).toHaveBeenCalledWith('/api/v1/admin/accounts', { params: { origin: 'local', offset: 0, limit: 40 } })
  })
  it('keeps large report cursors as strings and uses the actual slash endpoint', async () => {
    const get = vi.spyOn(client, 'get').mockResolvedValue({ data: [] })
    await api.reports({ resolved: 'false', account_id: '', target_account_id: '', max_id: '90071992547409931', limit: 40 })
    expect(get).toHaveBeenCalledWith('/api/v1/admin/reports/', { params: { resolved: 'false', max_id: '90071992547409931', limit: 40 } })
  })
  it('deletes a post through the admin endpoint without changing report state', async () => {
    const remove = vi.spyOn(client, 'delete').mockResolvedValue({})
    await api.deleteStatus('17')
    expect(remove).toHaveBeenCalledWith('/api/v1/admin/statuses/17')
  })
  it('sends account moderation using supported action and restoration endpoints', async () => {
    const post = vi.spyOn(client, 'post').mockResolvedValue({})
    await api.accountAction('12', 'disable')
    await api.accountAction('12', 'unsuspend')
    await api.reportAction('9', 'resolve')
    expect(post.mock.calls).toEqual([
      ['/api/v1/admin/accounts/12/action', { type: 'disable' }],
      ['/api/v1/admin/accounts/12/unsuspend'],
      ['/api/v1/admin/reports/9/resolve'],
    ])
  })
})
