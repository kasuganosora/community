import client from '@/shared/api/client'
import type { MastodonAccount } from '@/shared/types/mastodon'
import type { AuthCredentials, RegisterCredentials, LoginResponse } from '../domain/entities'

export const authApi = {
  async login(credentials: AuthCredentials): Promise<LoginResponse> {
    const { data } = await client.post<LoginResponse>('/api/v1/accounts/login', credentials)
    return data
  },

  async register(credentials: RegisterCredentials): Promise<{ message: string }> {
    const { data } = await client.post('/api/v1/accounts/register', credentials)
    return data
  },

  async verifyCredentials(): Promise<MastodonAccount> {
    const { data } = await client.get<MastodonAccount>('/api/v1/accounts/verify_credentials')
    return data
  },
}
