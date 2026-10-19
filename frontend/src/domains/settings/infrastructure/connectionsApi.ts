import client, { tokenService } from '@/shared/api/client'

export interface AccessToken { id: string; name: string; scopes: string[]; created_at: string; expires_at: string | null; last_used_at?: string | null; revoked_reason?: string }
export interface Webhook { id: string; name: string; url: string; events: string[]; enabled: boolean; created_at: string }
export interface HookDraft { name: string; url: string; secret: string; events: string[]; enabled: boolean }
export interface Delivery { id: string; event: string; state: 'pending' | 'processing' | 'delivered' | 'failed' | 'cancelled'; attempts: number; last_error: string; created_at: string }
const path = '/api/v1/settings'
export const connectionsApi = {
  async tokens() { return (await client.get<AccessToken[]>(`${path}/tokens`)).data },
  async createToken(input: { name: string; scopes: string[]; expires_in_days: number; password?: string; otp?: string }) { return (await client.post<{ token: string; item: AccessToken }>(`${path}/tokens`, input)).data },
  async updateToken(id: string, input: { name: string; scopes: string[]; password?: string; otp?: string }) { return (await client.patch<AccessToken>(`${path}/tokens/${encodeURIComponent(id)}`, input)).data },
  async revokeToken(id: string) { await client.delete(`${path}/tokens/${encodeURIComponent(id)}`) },
  async hooks() { return (await client.get<Webhook[]>(`${path}/webhooks`)).data },
  async saveHook(input: HookDraft, id?: string) {
    const url = `${path}/webhooks${id ? `/${encodeURIComponent(id)}` : ''}`
    return (await client.request<{ item: Webhook; secret: string }>({ method: id ? 'PUT' : 'POST', url, data: input })).data
  },
  async deleteHook(id: string) { await client.delete(`${path}/webhooks/${encodeURIComponent(id)}`) },
  async deliveries(id: string) { return (await client.get<Delivery[]>(`${path}/webhooks/${encodeURIComponent(id)}/deliveries`)).data },
  async testHook(id: string) { await client.post(`${path}/webhooks/${encodeURIComponent(id)}/test`) },
}

/** A standalone fetch deliberately avoids the application's global 401 handler. */
export async function runConsole(input: { path: string; method: string; body: string; token: string }, signal: AbortSignal) {
  if (!input.path.startsWith('/api/') || /[\\\r\n]/.test(input.path)) throw new Error('path')
  const url = new URL(input.path, window.location.origin)
  if (url.origin !== window.location.origin || !url.pathname.startsWith('/api/') || url.hash) throw new Error('path')
  let body: string | undefined
  if (!['GET', 'HEAD'].includes(input.method) && input.body.trim()) body = JSON.stringify(JSON.parse(input.body))
  const token = input.token.trim() || tokenService.get()
  if (!token) throw new Error('token')
  const started = performance.now()
  const response = await fetch(url.href, {
    method: input.method, body, signal, credentials: 'omit', redirect: 'error',
    headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' },
  })
  const reader = response.body?.getReader()
  const chunks: Uint8Array[] = []
  let bytes = 0
  try {
    while (reader) {
      const next = await reader.read()
      if (next.done) break
      bytes += next.value.byteLength
      if (bytes > 1024 * 1024) { await reader.cancel(); throw new Error('size') }
      chunks.push(next.value)
    }
  } finally { reader?.releaseLock() }
  const joined = new Uint8Array(bytes)
  let offset = 0
  for (const chunk of chunks) { joined.set(chunk, offset); offset += chunk.byteLength }
  let text = new TextDecoder().decode(joined)
  try { text = JSON.stringify(JSON.parse(text), null, 2) } catch { /* keep plain response */ }
  return { status: response.status, duration: Math.round(performance.now() - started), text }
}
