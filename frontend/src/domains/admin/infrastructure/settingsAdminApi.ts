import client from '@/shared/api/client'
export interface SettingField {
  deployment_key?: string
  key: string
  label: string
  type: 'text' | 'textarea' | 'email' | 'url' | 'number' | 'boolean' | 'secret'
  value?: string | number | boolean
  description?: string
  read_only: boolean
  source: 'database' | 'runtime' | 'environment'
  restart_required: boolean
  secret_set?: boolean
  options?: string[]
  min?: number
  max?: number
}
export interface SettingsSection {
  section: string
  title: string
  description: string
  fields: SettingField[]
}
export const settingsAdminApi = {
  async get(section: string) {
    return (await client.get<SettingsSection>(`/api/v1/admin/settings/${encodeURIComponent(section)}`)).data
  },
  async save(section: string, values: Record<string, string | number | boolean>, clear_secrets: string[]) {
    return (await client.put<SettingsSection>(`/api/v1/admin/settings/${encodeURIComponent(section)}`, { values, clear_secrets })).data
  },
  async test(section: 'email' | 'storage') {
    return (await client.post<{ success: boolean; message?: string }>(`/api/v1/admin/settings/${section}/test`, {}, { timeout: 30000 })).data
  },
}
