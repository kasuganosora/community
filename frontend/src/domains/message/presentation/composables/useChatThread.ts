import { sessionEpoch } from '@/shared/api/sessionScope'
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { chatApi } from '../../infrastructure/chatApi'
import type { ChatConversation, ChatMemberInput, ChatMessage, ChatSend } from '../../domain/chat'

export function useChatThread(id: () => string) {
  const auth = useAuthStore()
  const conversation = ref<ChatConversation | null>(null)
  const messages = ref<ChatMessage[]>([])
  const cursor = ref<string | null>(null)
  const loading = ref(false)
  const loadingOlder = ref(false)
  const pending = ref('')
  const error = ref('')
  let disposed = false
  let epoch = 0
  let readVersion = 0
  let timer: ReturnType<typeof setTimeout> | undefined
  const busy = computed(() => !!pending.value)
  const current = (version: number) => epoch === version
  function denied(e: unknown) {
    const code = (e as { response?: { status?: number } })?.response?.status
    return code === 401 || code === 403 || code === 404
  }
  function fail(e: unknown) {
    if (denied(e)) { conversation.value = null; messages.value = []; cursor.value = null }
    error.value = denied(e) ? '此会话已不可访问，请返回私信列表。' : e instanceof Error ? e.message : '请求失败，请重试。'
  }
  function merge(items: ChatMessage[]) {
    const byId = new Map(messages.value.map(message => [message.event_id, message]))
    for (const message of items) byId.set(message.event_id, message)
    messages.value = [...byId.values()].sort((a, b) => a.created_at - b.created_at)
  }
  async function refresh(initial = false) {
    if (!id() || (!initial && busy.value)) return
    const version = epoch
    const request = ++readVersion
    if (initial) loading.value = true
    try {
      const [info, page] = await Promise.all([chatApi.get(id()), chatApi.messages(id())])
      if (!current(version) || request !== readVersion) return
      conversation.value = info
      if (initial) { messages.value = page.messages; cursor.value = page.next_cursor }
      else merge(page.messages)
      error.value = ''
    } catch (e) { if (current(version) && request === readVersion) fail(e) }
    finally { if (current(version)) loading.value = false }
  }
  async function older() {
    if (!cursor.value || loadingOlder.value || busy.value) return
    const version = epoch
    const request = ++readVersion
    loadingOlder.value = true
    try {
      const page = await chatApi.messages(id(), cursor.value)
      if (!current(version) || request !== readVersion) return
      merge(page.messages)
      cursor.value = page.next_cursor
    } catch (e) { if (current(version) && request === readVersion) fail(e) }
    finally { if (current(version)) loadingOlder.value = false }
  }
  async function mutate<T>(name: string, action: () => Promise<T>, apply?: (result: T) => void): Promise<boolean> {
    if (busy.value || !conversation.value) return false
    const version = epoch
    readVersion++
    pending.value = name
    error.value = ''
    try {
      const result = await action()
      if (!current(version)) return false
      apply?.(result)
      pending.value = ''
      await refresh()
      return current(version)
    } catch (e) {
      if (current(version)) {
        // A forbidden mutation does not necessarily revoke access to the thread.
        // Recheck readable history before discarding the user's current context.
        pending.value = ''
        await refresh()
        if (current(version) && conversation.value) error.value = denied(e)
          ? '无法完成此操作。消息可能尚未处理完成，或你没有操作权限；草稿已保留。'
          : '操作失败，草稿已保留。请稍后重试。'
      }
      return false
    } finally { if (current(version)) pending.value = '' }
  }
  async function send(input: ChatSend, editing?: ChatMessage) {
    const room = id()
    return mutate('send', () => editing ? chatApi.replaceMessage(room, editing.event_id, input) : chatApi.send(room, input), result => {
      if (editing) messages.value = messages.value.map(message => message.event_id === editing.event_id ? { ...message, redacted: true, status: undefined, body: '' } : message)
      merge([{ event_id: result.event_id, sender: auth.currentUser?.acct || '你', body: input.body, msgtype: 'm.text', status_id: null, created_at: Date.now(), own: true, reply_event_id: input.reply_event_id, delivery: { event_id: result.event_id, room_id: conversation.value!.room_id, state: 'queued', remote_received: false, can_retry: false, updated_at: new Date().toISOString() } }])
    })
  }
  const remove = (message: ChatMessage) => mutate(message.event_id, () => chatApi.deleteMessage(id(), message.event_id), () => {
    messages.value = messages.value.map(item => item.event_id === message.event_id ? { ...item, redacted: true, status: undefined, body: '' } : item)
  })
  const retry = (message: ChatMessage) => mutate(message.event_id, () => chatApi.retryDelivery(id(), message.event_id), result => { message.delivery = result })
  const delivery = (message: ChatMessage) => mutate(message.event_id, () => chatApi.delivery(id(), message.event_id), result => { message.delivery = result })
  const addMembers = (members: ChatMemberInput[]) => mutate('members', () => chatApi.addMembers(id(), members), result => { conversation.value = result })
  const removeMember = (memberId: string) => mutate('members', () => chatApi.removeMember(id(), memberId))
  function poll() {
    if (disposed) return
    timer = setTimeout(async () => {
      if (document.visibilityState === 'visible' && conversation.value && !loadingOlder.value) await refresh()
      poll()
    }, 5000)
  }
  watch([id, () => sessionEpoch.value, () => auth.currentUser?.id], () => {
    epoch++; readVersion++; conversation.value = null; messages.value = []; cursor.value = null
    pending.value = ''; loading.value = false; loadingOlder.value = false; error.value = ''
    if (auth.currentUser && id()) void refresh(true)
  }, { immediate: true, flush: 'sync' })
  poll()
  onBeforeUnmount(() => { disposed = true; epoch++; readVersion++; clearTimeout(timer) })
  return { conversation, messages, cursor, loading, loadingOlder, pending, busy, error, refresh, older, send, remove, retry, delivery, addMembers, removeMember }
}
