import client from '@/shared/api/client'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { AuthCredentials, RegisterCredentials, LoginResponse, TwoFactorChallenge } from '../domain/entities'

function twoFactorChallengeOf(e: unknown): TwoFactorChallenge | null {
  const data = (e as { response?: { status?: number; data?: { error?: string; challenge?: string } } })?.response
  if (data?.status === 401 && data.data?.error === 'two_factor_required' && typeof data.data.challenge === 'string') {
    return { twoFactorRequired: true, challenge: data.data.challenge }
  }
  return null
}

export const authApi = {
  async login(credentials: AuthCredentials): Promise<LoginResponse | TwoFactorChallenge> {
    try {
      const { data } = await client.post<LoginResponse>('/api/v1/accounts/login', {
        username: credentials.username,
        password: credentials.password,
        captcha_response: credentials.captchaResponse || undefined,
      })
      return data
    } catch (e) {
      const challenge = twoFactorChallengeOf(e)
      if (challenge) return challenge
      throw e
    }
  },

  /** Second step of a password login for accounts with two-factor authentication. */
  async loginTwoFactor(challenge: string, code: string): Promise<LoginResponse> {
    const { data } = await client.post<LoginResponse>('/api/v1/accounts/login/two_factor', { challenge, code })
    return data
  },

  async register(credentials: RegisterCredentials): Promise<{ message: string; approval_required?: boolean }> {
    // The backend binds the Mastodon web-form field names (see
    // RegisterRequest in controllers/api/account/register.go); a plain JSON
    // {username,email,password} body always failed with 422 because
    // display_name and password_confirmation are required.
    const form = new URLSearchParams()
    form.set('user[account_attributes][username]', credentials.username)
    form.set('user[account_attributes][display_name]', credentials.displayName || credentials.username)
    form.set('user[email]', credentials.email)
    form.set('user[password]', credentials.password)
    form.set('user[password_confirmation]', credentials.passwordConfirm ?? credentials.password)
    if (credentials.inviteCode) form.set('user[invite_code]', credentials.inviteCode)
    if (credentials.captchaResponse) form.set('grecaptcha[response]', credentials.captchaResponse)
    const { data } = await client.post<{ message: string; approval_required?: boolean }>('/api/v1/accounts/register', form, {
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    })
    return data
  },

  async verifyCredentials(): Promise<MastodonAccount> {
    const { data } = await client.get<MastodonAccount>('/api/v1/accounts/verify_credentials')
    return data
  },

  /** Revoke the active token, or `token` when given (another signed-in account). */
  async logout(token?: string): Promise<void> {
    try {
      await client.get('/api/v1/accounts/logout', token ? { headers: { Authorization: `Bearer ${token}` } } : undefined)
    } catch {
      // Ignore error — we clear the token locally regardless
    }
  },

  async passkeyLoginBegin(): Promise<{ publicKey: Record<string, unknown> }> {
    const { data } = await client.post<{ publicKey: Record<string, unknown> }>('/api/v1/auth/passkey/login/begin', {})
    return data
  },

  async passkeyLoginFinish(credential: Record<string, unknown>): Promise<LoginResponse> {
    const { data } = await client.post<LoginResponse>('/api/v1/auth/passkey/login/finish', credential)
    return data
  },

  async passkeyRegisterBegin(name: string): Promise<{ publicKey: Record<string, unknown> }> {
    const { data } = await client.post<{ publicKey: Record<string, unknown> }>('/api/v1/auth/passkey/register/begin', { name })
    return data
  },

  async passkeyRegisterFinish(credential: Record<string, unknown>): Promise<{ id: string; name: string }> {
    const { data } = await client.post<{ id: string; name: string }>('/api/v1/auth/passkey/register/finish', credential)
    return data
  },

  async listPasskeys(): Promise<PasskeyItem[]> {
    const { data } = await client.get<PasskeyItem[]>('/api/v1/auth/passkeys')
    return data
  },

  async deletePasskey(id: string): Promise<void> {
    await client.delete(`/api/v1/auth/passkeys/${id}`)
  },

  async listAuthorizedApps(): Promise<AuthorizedApp[]> {
    const { data } = await client.get<AuthorizedApp[]>('/api/v1/oauth/authorized_applications')
    return data
  },

  async revokeAuthorizedApp(id: string): Promise<void> {
    await client.delete(`/api/v1/oauth/authorized_applications/${id}`)
  },
}

export interface PasskeyItem {
  id: string
  name: string
  created_at?: string
  last_used_at?: string
}

export interface AuthorizedApp {
  id: string
  name: string
  website?: string
  scopes?: string
  created_at?: string
}
