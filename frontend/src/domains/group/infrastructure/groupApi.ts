import client from '@/shared/api/client'
import type { Group, GroupPage, GroupSearchPage, GroupMember, GroupInvitee, GroupMessagePage, GroupSend, GroupSendResult, GroupJoinResult, GroupSync, GroupUpload } from '../domain/group'
import { t } from '@/shared/i18n'
const base = '/api/v1/chat/groups'
const path = (id: string) => `${base}/${encodeURIComponent(id)}`
// Outlive the server's 300s transfer budget and its 5s socket-response margin.
const mediaTimeout = 310000
export const groupApi = {
  async invitees(q: string, signal?: AbortSignal): Promise<GroupInvitee[]> { return (await client.get<{ users: GroupInvitee[] }>(`${base}/invitees`, { params: { q, limit: 8 }, signal })).data.users },
  async list(cursor?: string, signal?: AbortSignal): Promise<GroupPage> { return (await client.get(base, { params: { cursor, limit: 40 }, signal })).data },
  async get(id: string, signal?: AbortSignal): Promise<Group> { return (await client.get(path(id), { signal })).data },
  async create(input: { transaction_id: string; name: string; topic?: string; visibility: 'private' | 'public'; invite?: string[]; plaintext_consent: true }): Promise<Group> { return (await client.post(base, input, { timeout: 35000 })).data },
  async update(id: string, input: { name?: string; topic?: string; avatar_upload_id?: string }): Promise<Group> { return (await client.patch(path(id), input)).data },
  async address(id: string, alias: string): Promise<Group> { return (await client.put(`${path(id)}/address`, { alias })).data },
  async search(params: { q: string; scope: 'joined' | 'local' | 'remote'; server?: string; cursor?: string; limit?: number }, signal?: AbortSignal): Promise<GroupSearchPage> { return (await client.get(`${base}/search`, { params, signal, timeout: 35000 })).data },
  async resolve(target: string, signal?: AbortSignal, via?: string[], preview_ref?: string): Promise<{ group?: Group; target: string; via: string[]; client_url?: string }> { return (await client.post(`${base}/resolve`, { target, via, preview_ref }, { signal, timeout: 35000 })).data },
  async join(input: { target: string; via?: string[]; action?: 'join' | 'knock' }, signal?: AbortSignal): Promise<GroupJoinResult> { return (await client.post(`${base}/join`, input, { signal, timeout: 90000 })).data },
  async joinStatus(id: string, signal?: AbortSignal): Promise<GroupJoinResult> { return (await client.get(`${base}/join/${encodeURIComponent(id)}`, { signal })).data },
  async members(id: string, cursor?: string): Promise<{ members: GroupMember[]; next_cursor: string | null }> { return (await client.get(`${path(id)}/members`, { params: { cursor, limit: 50 } })).data },
  async member(id: string, input: { user_id: string; action: 'invite' | 'kick' | 'ban' | 'unban' | 'approve'; reason?: string }): Promise<Group> { return (await client.post(`${path(id)}/members`, input)).data },
  async permissions(id: string, user_id: string, power_level: number): Promise<Group> { return (await client.post(`${path(id)}/permissions`, { user_id, power_level })).data },
  async leave(id: string): Promise<void> { await client.post(`${path(id)}/leave`) },
  async preferences(id: string, muted: boolean): Promise<void> { await client.post(`${path(id)}/preferences`, { muted }) },
  async messages(id: string, before?: string, signal?: AbortSignal): Promise<GroupMessagePage> { return (await client.get(`${path(id)}/messages`, { params: { before, limit: 40 }, signal })).data },
  async send(id: string, input: GroupSend): Promise<GroupSendResult> { return (await client.post(`${path(id)}/messages`, input, { timeout: 60000 })).data },
  async replace(id: string, eventId: string, input: GroupSend): Promise<GroupSendResult> { return (await client.post(`${path(id)}/messages/${encodeURIComponent(eventId)}/replace`, input, { timeout: 60000 })).data },
  async remove(id: string, eventId: string): Promise<void> { await client.delete(`${path(id)}/messages/${encodeURIComponent(eventId)}`) },
  async reaction(id: string, eventId: string, key: string, own: boolean): Promise<void> {
    const url = `${path(id)}/messages/${encodeURIComponent(eventId)}/reactions`
    if (own) await client.delete(url, { params: { key } })
    else await client.post(url, { key, transaction_id: crypto.randomUUID() })
  },
  async read(id: string, event_id: string): Promise<void> { await client.post(`${path(id)}/read`, { event_id }) },
  async typing(id: string, typing: boolean): Promise<void> { await client.post(`${path(id)}/typing`, { typing }) },
  async upload(id: string, file: File, signal: AbortSignal, onProgress: (percent: number) => void): Promise<GroupUpload> {
    const form = new FormData(); form.append('file', file)
    return (await client.post(`${path(id)}/uploads`, form, { headers: { 'Content-Type': undefined }, signal, timeout: mediaTimeout, onUploadProgress: event => { if (event.total) onProgress(Math.round(event.loaded * 100 / event.total)) } })).data
  },
  async sync(since: string | undefined, signal: AbortSignal): Promise<GroupSync> { return (await client.post('/api/v1/chat/group-sync', { since, timeout: 25000 }, { signal, timeout: 35000 })).data },
  async media(url: string, signal?: AbortSignal): Promise<Blob> {
    // Authenticated media must stay on our own group API. Never forward the Community token to a remote URL.
    const parsed = new URL(url, window.location.origin)
    if (parsed.origin !== window.location.origin || !parsed.pathname.startsWith(`${base}/`)) throw new Error(t('group.groupApi.s1'))
    return (await client.get(parsed.pathname + parsed.search, { responseType: 'blob', signal, timeout: mediaTimeout })).data
  },
}
