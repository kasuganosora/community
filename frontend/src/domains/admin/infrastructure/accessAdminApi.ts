import client from '@/shared/api/client'
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
export const permissionLabels: Record<string, string> = { dashboard: '统计概览', users: '账号管理', reports: '举报与帖子审核', federation: '联邦管理', settings: '站点设置', blocks: '访问限制', rules: '规则管理', announcements: '公告管理', emojis: '表情管理', invites: '邀请管理', roles: '角色管理', system: '系统运维', audit: '审计日志', webhooks: 'Webhook 管理', media: '文件管理', branding: '头像装饰与广告' }
