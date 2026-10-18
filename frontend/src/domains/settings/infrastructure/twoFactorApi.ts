import client from '@/shared/api/client'

export interface TwoFactorStatus {
  enabled: boolean
  enabled_at: string | null
  backup_codes_remaining: number
}

export interface TwoFactorSetup {
  secret: string
  otpauth_uri: string
}

const path = '/api/v1/accounts/two_factor'

export const twoFactorApi = {
  async status() { return (await client.get<TwoFactorStatus>(path)).data },
  async setup(password: string) { return (await client.post<TwoFactorSetup>(`${path}/setup`, { password })).data },
  async confirm(code: string) { return (await client.post<{ enabled: boolean; backup_codes: string[] }>(`${path}/confirm`, { code })).data },
  async regenerateBackupCodes(password: string, code: string) {
    return (await client.post<{ backup_codes: string[] }>(`${path}/backup_codes`, { password, code })).data
  },
  async disable(password: string, code: string) { await client.post(`${path}/disable`, { password, code }) },
}
