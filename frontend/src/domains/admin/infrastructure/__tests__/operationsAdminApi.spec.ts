import { beforeEach, describe, expect, it, vi } from 'vitest'
import { operationsAdminApi as api } from '../operationsAdminApi'
import client from '@/shared/api/client'
vi.mock('@/shared/api/client', () => ({ default: { get: vi.fn(), post: vi.fn(), put: vi.fn(), delete: vi.fn() } }))
beforeEach(() => { vi.resetAllMocks(); vi.mocked(client.put).mockResolvedValue({ data: {} }); vi.mocked(client.post).mockResolvedValue({ data: {} }); vi.mocked(client.delete).mockResolvedValue({ data: {} }) })
describe('administrator operations', () => {
  it('preserves write-only secret and hidden captcha settings on ordinary edits', async () => {
    await api.saveCaptcha({ enabled: true, provider: 'hcaptcha', site_key: 'public' }, '')
    expect(client.put).toHaveBeenCalledWith('/api/v1/admin/captcha/config', { enabled: true, provider: 'hcaptcha', site_key: 'public' })
    await api.saveCaptcha({ enabled: true, provider: 'hcaptcha', site_key: 'public' }, 'replacement')
    expect(client.put).toHaveBeenLastCalledWith('/api/v1/admin/captcha/config', { enabled: true, provider: 'hcaptcha', site_key: 'public', secret_key: 'replacement' })
  })
  it('uses actual rule writes and propagates failure for draft recovery', async () => {
    vi.mocked(client.put).mockRejectedValueOnce(new Error('denied'))
    await expect(api.saveRule('8', 'draft')).rejects.toThrow('denied')
    expect(client.put).toHaveBeenCalledWith('/api/v1/admin/rule/8', { text: 'draft' })
  })
  it('does not invent enabled flags or start stop commands', async () => {
    await api.relayAction(3, 'disable'); await api.relayAction(3, 'delete')
    expect(client.post).toHaveBeenCalledWith('/api/v1/admin/relays/3/disable')
    expect(client.delete).toHaveBeenCalledWith('/api/v1/admin/relays/3')
    vi.mocked(client.post).mockResolvedValueOnce({ data: { success: false } })
    expect(await api.matrixTest('@qa:example.org')).toEqual({ success: false })
  })
  it('preserves real unhealthy 503 health payloads rather than hiding service status', async () => {
    vi.mocked(client.get).mockResolvedValue({ data: { status: 'unhealthy', services: {} } })
    expect(await api.health()).toEqual({ status: 'unhealthy', services: {} })
    const options = vi.mocked(client.get).mock.calls[0]![1]!
    expect(options.validateStatus!(503)).toBe(true)
    expect(options.validateStatus!(401)).toBe(false)
  })

})
