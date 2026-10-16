import client from '@/shared/api/client'
import { t } from '@/shared/i18n'
export interface AdminRole { id: string; name: string; color: string; highlighted: boolean; permissions: string[]; protected: boolean }
export type RoleDraft = Pick<AdminRole, 'name' | 'color' | 'highlighted' | 'permissions'>
export interface RoleMember { account_id: string; username: string }
export interface AdminInvite { id: string; code: string; account_id: string; uses: number; max_uses: number; expires_at: string | null; created_at: string; disabled: boolean; available: boolean }
interface Page<T> { items: T[]; total: number }
const base = '/api/v1/admin'
export const accessAdminApi = {
  async roles(offset = 0) { return (await client.get<Page<AdminRole> & { available_permissions: string[] }>(`${base}/roles`, { params: { limit: 40, offset } })).data },
  async saveRole(id: string | null, body: RoleDraft) { return id ? (await client.put<AdminRole>(`${base}/roles/${encodeURIComponent(id)}`, body)).data : (await client.post<AdminRole>(`${base}/roles`, body)).data },
  async deleteRole(id: string) { await client.delete(`${base}/roles/${encodeURIComponent(id)}`) },
  async members(id: string, offset = 0) { return (await client.get<Page<RoleMember>>(`${base}/roles/${encodeURIComponent(id)}/users`, { params: { limit: 40, offset } })).data },
  async changeMember(id: string, account: string, assign: boolean) { const url = `${base}/roles/${encodeURIComponent(id)}/users/${encodeURIComponent(account)}`; if (assign) await client.post(url); else await client.delete(url) },
  async invites(state = '', offset = 0) { return (await client.get<Page<AdminInvite>>(`${base}/invites`, { params: { state, limit: 40, offset } })).data },
  async createInvites(body: { count: number; max_uses: number; expires_in: number }) { return (await client.post<{ items: AdminInvite[] }>(`${base}/invites`, body)).data },
  async setInviteDisabled(id: string, disabled: boolean) { return (await client.put<AdminInvite>(`${base}/invites/${encodeURIComponent(id)}`, { disabled })).data },
}
export const permissionLabels: Record<string, string> = { dashboard: t('admin.accessAdminApi.s1'), users: t('admin.accessAdminApi.s2'), reports: t('admin.accessAdminApi.s3'), federation: t('admin.accessAdminApi.s4'), settings: t('admin.accessAdminApi.s5'), blocks: t('admin.accessAdminApi.s6'), rules: t('admin.accessAdminApi.s7'), announcements: t('admin.accessAdminApi.s8'), emojis: t('admin.accessAdminApi.s9'), invites: t('admin.accessAdminApi.s10'), roles: t('admin.accessAdminApi.s11'), system: t('admin.accessAdminApi.s12'), audit: t('admin.accessAdminApi.s13'), webhooks: t('admin.accessAdminApi.s14'), media: t('admin.accessAdminApi.s15'), branding: t('admin.accessAdminApi.s16') }
