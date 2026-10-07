import { beforeEach, describe, expect, it, vi } from 'vitest'
import client from '@/shared/api/client'
import { accessAdminApi } from '../accessAdminApi'
vi.mock('@/shared/api/client', () => ({ default: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() } }))
beforeEach(() => vi.clearAllMocks())
describe('administrative access API', () => {
  it('uses account IDs for role membership', async () => {
    vi.mocked(client.post).mockResolvedValue({ data: {} }); vi.mocked(client.delete).mockResolvedValue({ data: {} })
    await accessAdminApi.changeMember('3', '42', true); await accessAdminApi.changeMember('3', '42', false)
    expect(client.post).toHaveBeenCalledWith('/api/v1/admin/roles/3/users/42'); expect(client.delete).toHaveBeenCalledWith('/api/v1/admin/roles/3/users/42')
  })
  it('sends explicit limits and mutates only disabled state', async () => {
    vi.mocked(client.post).mockResolvedValue({ data: { items: [] } }); vi.mocked(client.put).mockResolvedValue({ data: {} })
    await accessAdminApi.createInvites({ count: 2, max_uses: 1, expires_in: 3600 }); await accessAdminApi.setInviteDisabled('4', true)
    expect(client.post).toHaveBeenCalledWith('/api/v1/admin/invites', { count: 2, max_uses: 1, expires_in: 3600 }); expect(client.put).toHaveBeenCalledWith('/api/v1/admin/invites/4', { disabled: true })
  })
})
