import client from '@/shared/api/client'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { AuthCredentials, RegisterCredentials, LoginResponse } from '../domain/entities'

export const authApi = {
  async login(credentials: AuthCredentials): Promise<LoginResponse> {
    const { data } = await client.post<LoginResponse>('/api/v1/accounts/login', credentials)
    return data
  },

  async register(credentials: RegisterCredentials): Promise<{ message: string }> {
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
    const { data } = await client.post<{ message: string }>('/api/v1/accounts/register', form, {
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
    })
    return data
  },

  async verifyCredentials(): Promise<MastodonAccount> {
    const { data } = await client.get<MastodonAccount>('/api/v1/accounts/verify_credentials')
    return data
  },

  async logout(): Promise<void> {
    try {
      await client.get('/api/v1/accounts/logout')
    } catch {
      // Ignore error — we clear the token locally regardless
    }
  },
}
