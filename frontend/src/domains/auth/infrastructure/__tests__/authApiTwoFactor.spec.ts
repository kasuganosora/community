import { describe, it, expect, vi } from 'vitest'

const post = vi.hoisted(() => vi.fn())
vi.mock('@/shared/api/client', () => ({ default: { post } }))

import { authApi } from '../authApi'

describe('authApi two-factor login', () => {
  it('turns two_factor_required into a challenge instead of an error', async () => {
    post.mockRejectedValueOnce({ response: { status: 401, data: { error: 'two_factor_required', challenge: 'abc' } } })
    await expect(authApi.login({ username: 'qa', password: 'pw' })).resolves.toEqual({ twoFactorRequired: true, challenge: 'abc' })
  })

  it('keeps wrong passwords as errors', async () => {
    const err = { response: { status: 401, data: { error: 'invalid username or password' } } }
    post.mockRejectedValueOnce(err)
    await expect(authApi.login({ username: 'qa', password: 'bad' })).rejects.toBe(err)
  })

  it('posts the challenge and code to the second step', async () => {
    post.mockResolvedValueOnce({ data: { message: 'login success', token: 't' } })
    await expect(authApi.loginTwoFactor('abc', '123456')).resolves.toEqual({ message: 'login success', token: 't' })
    expect(post).toHaveBeenLastCalledWith('/api/v1/accounts/login/two_factor', { challenge: 'abc', code: '123456' })
  })
})
