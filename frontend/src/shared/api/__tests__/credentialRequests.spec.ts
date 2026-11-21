import { expect, it } from 'vitest'
import client from '@/shared/api/client'
import { authApi } from '@/domains/auth/infrastructure/authApi'
import { accountLifecycleApi } from '@/domains/settings/infrastructure/accountLifecycleApi'
import { twoFactorApi } from '@/domains/settings/infrastructure/twoFactorApi'
import { connectionsApi } from '@/domains/settings/infrastructure/connectionsApi'
import { settingsAdminApi } from '@/domains/admin/infrastructure/settingsAdminApi'
import { userApi } from '@/domains/user/infrastructure/userApi'

it('preserves the configured API origin and rejects redirects on credential writes', async () => {
  const originalBase = client.defaults.baseURL
  const originalEnv = client.defaults.env
  const origin = 'https://api.example.test'
  const paths: string[] = []
  client.defaults.baseURL = origin
  client.defaults.env = { ...originalEnv, fetch: async (input) => {
    const request = input as Request
    const url = new URL(request.url)
    expect(url.origin).toBe(origin)
    expect(request.redirect).toBe('error')
    expect(request.credentials).toBe('include')
    if (url.pathname.endsWith('/register')) {
      expect(request.headers.get('Content-Type')).toContain('application/x-www-form-urlencoded')
      expect(new URLSearchParams(await request.text()).get('user[account_attributes][username]')).toBe('tester')
    }
    paths.push(request.method + ' ' + url.pathname)
    return new Response('{}', { headers: { 'Content-Type': 'application/json' } })
  } }
  try {
    await authApi.login({ username: 'tester', password: 'fixture' })
    await authApi.loginTwoFactor('fixture', 'fixture')
    await authApi.register({ username: 'tester', email: 'tester@example.test', password: 'fixture' })
    await authApi.refreshPersistentSession('fixture')
    await authApi.revokePersistentSession('fixture', false)
    await authApi.passkeyLoginFinish({})
    await authApi.passkeyRegisterFinish({})
    await accountLifecycleApi.move('tester@example.test', 'fixture', 'fixture')
    await twoFactorApi.setSecurityPin('fixture', 'fixture', 'fixture')
    await userApi.changePassword('fixture', 'fixture')
    await connectionsApi.updateToken('17', { name: 'fixture', scopes: [], password: 'fixture' })
    await connectionsApi.saveHook({ name: 'test', url: origin, secret: 'fixture', events: [], enabled: true }, '7')
    await settingsAdminApi.save('mail', { password: 'fixture' }, [])
    expect(paths).toEqual([
      'POST /api/v1/accounts/login', 'POST /api/v1/accounts/login/two_factor', 'POST /api/v1/accounts/register',
      'POST /api/v1/accounts/session/refresh', 'POST /api/v1/accounts/persistent/revoke',
      'POST /api/v1/auth/passkey/login/finish', 'POST /api/v1/auth/passkey/register/finish',
      'POST /api/v1/accounts/lifecycle/move', 'POST /api/v1/accounts/two_factor/security_pin',
      'PUT /api/v1/accounts/me/password', 'PATCH /api/v1/settings/tokens/17',
      'PUT /api/v1/settings/webhooks/7', 'PUT /api/v1/admin/settings/mail',
    ])
  } finally {
    client.defaults.env = originalEnv
    client.defaults.baseURL = originalBase
  }
})
