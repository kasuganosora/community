import client from '@/shared/api/client'

export interface AccountLifecycleState {
  aliases: string[]
  moved_to: string
  deletion_requested: boolean
}
const path = '/api/v1/accounts/lifecycle'
export const accountLifecycleApi = {
  async get() { return (await client.get<AccountLifecycleState>(path)).data },
  async saveAliases(aliases: string[]) { return (await client.put<AccountLifecycleState>(`${path}/aliases`, { aliases }, { timeout: 25000 })).data },
  async move(target: string, password: string, otp = '') { return (await client.post<AccountLifecycleState>(`${path}/move`, { target, password, confirm: true, otp_attempt: otp || undefined }, { timeout: 25000 })).data },
  async delete(password: string, confirmation: string, otp = '') { await client.post(`${path}/delete`, { password, confirmation, confirm: true, otp_attempt: otp || undefined }) },
}
