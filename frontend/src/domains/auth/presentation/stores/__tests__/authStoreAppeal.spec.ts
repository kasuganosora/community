import { beforeEach, describe, expect, it, vi } from 'vitest'
import { createPinia, setActivePinia } from 'pinia'
import { markAppealSession, clearAppealSession } from '@/shared/api/appealSession'
import { tokenService } from '@/shared/api/client'

const api = vi.hoisted(() => ({ logout: vi.fn(async () => {}), verifyCredentials: vi.fn() }))
vi.mock('../../../infrastructure/authApi', () => ({ authApi: api }))
vi.mock('@/shared/composables/useMediaMode', () => ({ useMediaMode: () => ({ loadMode: vi.fn() }) }))

import { useAuthStore } from '../authStore'

describe('auth store suspended appeal', () => {
  beforeEach(() => {
    localStorage.clear()
    clearAppealSession()
    vi.clearAllMocks()
    setActivePinia(createPinia())
    tokenService.set('kept')
  })

  it('does not drop the token when verify fails for an appeal session', async () => {
    api.verifyCredentials.mockImplementation(async () => {
      markAppealSession()
      throw Object.assign(new Error('unauthorized'), { response: { status: 401 } })
    })
    const store = useAuthStore()
    expect(await store.initialize()).toBe(false)
    expect(tokenService.get()).toBe('kept')
    expect(store.token).toBe('kept')
    expect(api.logout).not.toHaveBeenCalled()
  })
})
