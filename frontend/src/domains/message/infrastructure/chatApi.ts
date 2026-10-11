import client from '@/shared/api/client'
import type { ChatConversation, ChatDelivery, ChatMemberInput, ChatMessagePage, ChatSend } from '../domain/chat'

const base = '/api/v1/chat/conversations'
const path = (id: string) => `${base}/${encodeURIComponent(id)}`
export const chatApi = {
  async list(cursor?: string): Promise<{ conversations: ChatConversation[]; next_cursor: string | null }> {
    const { data } = await client.get(base, { params: { ...(cursor ? { cursor } : {}) } })
    return data
  },
  async create(transactionId: string, members: ChatMemberInput[]): Promise<ChatConversation> {
    const { data } = await client.post<ChatConversation>(base, { transaction_id: transactionId, plaintext_consent: true, members })
    return data
  },
  async get(id: string): Promise<ChatConversation> {
    const { data } = await client.get<ChatConversation>(path(id))
    return data
  },
  async addMembers(id: string, members: ChatMemberInput[]): Promise<ChatConversation> {
    const { data } = await client.post<ChatConversation>(`${path(id)}/members`, { plaintext_consent: true, members })
    return data
  },
  async removeMember(id: string, memberId: string): Promise<void> {
    await client.delete(`${path(id)}/members/${encodeURIComponent(memberId)}`)
  },
  async messages(id: string, before?: string): Promise<ChatMessagePage> {
    const { data } = await client.get<ChatMessagePage>(`${path(id)}/messages`, { params: { limit: 40, ...(before ? { before } : {}) } })
    return data
  },
  async deleteMessage(id: string, eventId: string): Promise<void> {
    await client.delete(`${path(id)}/messages/${encodeURIComponent(eventId)}`)
  },
  async replaceMessage(id: string, eventId: string, message: ChatSend): Promise<{ event_id: string; event_ids: string[]; delivery: 'queued' }> {
    const { data } = await client.post(`${path(id)}/messages/${encodeURIComponent(eventId)}/replace`, message)
    return data
  },
  async delivery(id: string, eventId: string): Promise<ChatDelivery> {
    const { data } = await client.get<ChatDelivery>(`${path(id)}/messages/${encodeURIComponent(eventId)}/delivery`)
    return data
  },
  async retryDelivery(id: string, eventId: string): Promise<ChatDelivery> {
    const { data } = await client.post<ChatDelivery>(`${path(id)}/messages/${encodeURIComponent(eventId)}/retry`)
    return data
  },
  async send(id: string, message: ChatSend): Promise<{ event_id: string; event_ids: string[]; delivery: 'queued' }> {
    const { data } = await client.post(`${path(id)}/messages`, message)
    return data
  },
}
