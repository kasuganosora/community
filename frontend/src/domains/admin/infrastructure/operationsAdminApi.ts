import client from '@/shared/api/client'
export interface SiteRule { id: string; text: string }
export interface CaptchaConfig { enabled: boolean; provider: string; site_key: string; effective_enabled?: boolean; environment_override?: boolean }
export interface Relay { id: number; inbox_url: string; state: string; created_at: string; updated_at: string }
export interface MatrixStatus { enabled: boolean; connected: boolean; homeserver?: string; server_name?: string; message: string }
export interface MatrixConfig { enabled: boolean; homeserver_url: string; server_name: string; bridge_username: string; user_namespace: string; room_namespace: string }
export interface ServiceHealth { status: string; latency: string; message?: string }
export interface DetailedHealth { status: string; timestamp: string; uptime: string; services: Record<string, ServiceHealth> }
const base = '/api/v1/admin'
export const operationsAdminApi = {
  async health() { return (await client.get<DetailedHealth>('/health/detailed', { validateStatus: status => status === 200 || status === 503 })).data },
  async matrixVersions() { return (await client.get<{ versions: string[] }>('/_matrix/client/versions')).data },
  async rules() { return (await client.get<SiteRule[]>('/api/v1/instance/rules')).data },
  async saveRule(id: string | null, text: string) { return id ? (await client.put<SiteRule>(`${base}/rule/${encodeURIComponent(id)}`, { text })).data : (await client.post<SiteRule>(`${base}/rule`, { text })).data },
  async deleteRule(id: string) { await client.delete(`${base}/rule/${encodeURIComponent(id)}`) },
  async captcha() { return (await client.get<CaptchaConfig>(`${base}/captcha/config`)).data },
  async saveCaptcha(config: CaptchaConfig, secret: string) {
    // Unchanged write-only credentials and unreadable thresholds must be omitted.
    await client.put(`${base}/captcha/config`, { enabled: config.enabled, provider: config.provider, site_key: config.site_key, ...(secret ? { secret_key: secret } : {}) })
  },
  async relays() { return (await client.get<Relay[]>(`${base}/relays`)).data },
  async addRelay(inbox_url: string) { return (await client.post<Relay>(`${base}/relays`, { inbox_url })).data },
  async relayAction(id: number, action: 'enable' | 'disable' | 'delete') { if (action === 'delete') await client.delete(`${base}/relays/${id}`); else await client.post(`${base}/relays/${id}/${action}`) },
  async matrixStatus() { return (await client.get<MatrixStatus>(`${base}/matrix/status`)).data },
  async matrixConfig() { return (await client.get<MatrixConfig>(`${base}/matrix/config`)).data },
  async matrixTest(matrix_user_id: string) { return (await client.post<{ success: boolean; display_name?: string }>(`${base}/matrix/test`, { matrix_user_id })).data },
}
