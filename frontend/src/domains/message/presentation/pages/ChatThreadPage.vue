<script setup lang="ts">
import { sessionEpoch } from '@/shared/api/sessionScope'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { DialogPlugin } from 'tdesign-vue-next'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import type { MastodonAttachment } from '@/shared/types/mastodon'
import type { ChatMember, ChatMessage, ChatMemberInput } from '../../domain/chat'
import { useChatThread } from '../composables/useChatThread'
import ChatMessageView from '../components/ChatMessageView.vue'
import ConversationMembersForm from '../components/ConversationMembersForm.vue'

const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const id = () => String(route.params.conversationId || '')
const thread = useChatThread(id)
const { conversation, messages, cursor, loading, loadingOlder, pending, busy, error } = thread
const composer = ref<HTMLFormElement>()
const draft = ref('')
const reply = ref<ChatMessage | undefined>()
const editing = ref<ChatMessage | undefined>()
const attachments = ref<(MastodonAttachment & { uploadName?: string })[]>([])
const uploading = ref(false)
const uploadError = ref('')
const membersOpen = ref(false)
const inviting = ref(false)
let uploadGeneration = 0
let transactionId = crypto.randomUUID()
onBeforeUnmount(() => { uploadGeneration++ })
const sendable = computed(() => (!!draft.value.trim() || attachments.value.length > 0) && !busy.value && !uploading.value)
watch([draft, () => attachments.value.map(file => file.id).join(','), () => reply.value?.event_id, () => editing.value?.event_id], () => { transactionId = crypto.randomUUID() }, { flush: 'sync' })
watch([id, () => sessionEpoch.value], () => {
  uploadGeneration++; draft.value = ''; attachments.value = []; reply.value = undefined; editing.value = undefined
  uploading.value = false; uploadError.value = ''; membersOpen.value = false; inviting.value = false
}, { flush: 'sync' })
async function send() {
  if (!sendable.value) return
  const epoch = sessionEpoch.value
  const room = id()
  const ok = await thread.send({ transaction_id: transactionId, body: draft.value, ...(reply.value ? { reply_event_id: reply.value.event_id } : {}), ...(attachments.value.length ? { media_ids: attachments.value.map(file => file.id) } : {}) }, editing.value)
  if (ok && epoch === sessionEpoch.value && room === id()) {
    draft.value = ''; attachments.value = []; reply.value = undefined; editing.value = undefined
  }
}
async function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  if (!files.length || busy.value || uploading.value) return
  const version = ++uploadGeneration
  uploading.value = true
  uploadError.value = ''
  try {
    for (const file of files) {
      const media = await postApi.uploadMedia(file)
      if (version !== uploadGeneration) return
      attachments.value.push({ ...media, uploadName: file.name })
    }
  } catch { if (version === uploadGeneration) uploadError.value = '附件上传失败，已成功上传的附件保留。请重试。' }
  finally { if (version === uploadGeneration) uploading.value = false }
}
watch(() => messages.value.at(-1)?.event_id, async (next, previous) => {
  if (!next) return
  const nearEnd = !previous || window.scrollY + window.innerHeight >= document.documentElement.scrollHeight - 180
  const room = id(), epoch = sessionEpoch.value
  await nextTick()
  if (nearEnd && room === id() && epoch === sessionEpoch.value) composer.value?.scrollIntoView?.({ block: 'end' })
})
function replyTo(message: ChatMessage) {
  reply.value = message; editing.value = undefined
  composer.value?.querySelector('textarea')?.focus()
}

function edit(message: ChatMessage) {
  editing.value = message; reply.value = undefined; attachments.value = []; draft.value = message.body
  document.querySelector<HTMLTextAreaElement>('.chat-compose textarea')?.focus()
}
function remove(message: ChatMessage) {
  const room = id(), epoch = sessionEpoch.value
  const dialog = DialogPlugin.confirm({ header: '删除这条消息？', body: '将请求删除原消息。对方可能已经阅读或保存了内容。', confirmBtn: '删除', cancelBtn: '取消',
    onConfirm: async () => { if (room === id() && epoch === sessionEpoch.value) await thread.remove(message); dialog.destroy() }, onClose: () => dialog.destroy(),
  })
}
function removeMember(member: ChatMember) {
  const self = member.account_id === auth.currentUser?.id
  const room = id(), epoch = sessionEpoch.value
  const dialog = DialogPlugin.confirm({ header: self ? '离开此会话？' : '移除此成员？', body: '该成员将不能继续通过此会话接收后续消息。已经收到的内容无法收回。', confirmBtn: self ? '离开' : '移除', cancelBtn: '取消',
    onConfirm: async () => { if (room === id() && epoch === sessionEpoch.value && await thread.removeMember(member.id) && self) await router.push('/messages'); dialog.destroy() }, onClose: () => dialog.destroy(),
  })
}
async function invite(members: ChatMemberInput[]) { if (await thread.addMembers(members)) inviting.value = false }
</script>

<template>
  <div class="chat-thread">
    <header class="page-header page-header--with-back">
      <router-link class="page-header__back-btn" to="/messages" aria-label="返回私信"><t-icon name="chevron-left" :size="20" /></router-link>
      <h1 class="page-header__title">{{ conversation?.title || '会话' }}</h1>
      <button v-if="conversation" class="btn btn--ghost btn--sm" :aria-expanded="membersOpen" @click="membersOpen = !membersOpen">成员</button>
    </header>
    <p class="chat-thread__notice">此会话不使用端到端加密，参与站点的服务器可以读取内容。</p>
    <p v-if="error" class="chat-thread__error" role="alert">{{ error }} <button class="btn btn--ghost btn--sm" @click="thread.refresh(!conversation)">重试</button></p>
    <div v-if="loading" class="page-loading"><t-loading /></div>
    <template v-if="conversation">
      <section v-if="membersOpen" class="chat-members" aria-label="会话成员">
        <h2>成员</h2>
        <p>新成员不能读取加入前的历史。移除成员不会收回其已保存的消息。</p>
        <ul>
<li v-for="member in conversation.members.filter(item => item.active !== false)" :key="member.id">
          <span>{{ member.display_name || member.acct || member.matrix_id || `账号 ${member.account_id}` }} <small v-if="member.acct">@{{ member.acct }} · </small><small>{{ member.kind === 'matrix' ? 'Matrix' : member.kind === 'ap' ? 'ActivityPub' : 'Community' }}</small></span>
          <button v-if="member.account_id === auth.currentUser?.id ? conversation.can_leave : conversation.can_manage" class="btn btn--ghost btn--sm" :disabled="busy" @click="removeMember(member)">{{ member.account_id === auth.currentUser?.id ? '离开' : '移除' }}</button>
        </li>
</ul>
        <button v-if="conversation.can_manage && !inviting" class="btn btn--secondary btn--sm" @click="inviting = true">添加成员</button>
        <ConversationMembersForm v-if="inviting" mode="invite" :pending="busy" :error="error" @submit="invite" @cancel="inviting = false" />
      </section>
      <div v-if="cursor" class="chat-thread__older"><button class="btn btn--ghost btn--sm" :disabled="loadingOlder || busy" @click="thread.older">{{ loadingOlder ? '加载中…' : '查看更早的消息' }}</button></div>
      <p v-if="!messages.length" class="chat-thread__empty">会话已创建，发送第一条消息吧。</p>
      <div aria-label="聊天消息">
        <ChatMessageView v-for="message in messages" :key="message.event_id" :message="message" :pending="busy || uploading" :reply="messages.find(item => item.event_id === message.reply_event_id)" @reply="replyTo(message)" @edit="edit(message)" @remove="remove(message)" @retry="thread.retry(message)" @refresh-delivery="thread.delivery(message)" />
      </div>
      <form ref="composer" class="chat-compose" @submit.prevent="send">
        <p v-if="editing" class="chat-compose__context" role="note">编辑会删除原消息并重新发送，消息链接和回应记录将改变。<button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="editing = undefined; draft = ''">取消编辑</button></p>
        <p v-else-if="reply" class="chat-compose__context">回复：{{ reply.body }} <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="reply = undefined">取消回复</button></p>
        <label class="sr-only" for="chat-message-input">消息内容</label>
        <textarea id="chat-message-input" v-model="draft" rows="3" placeholder="写一条消息…" :disabled="busy" @keydown.ctrl.enter.prevent="send" @keydown.meta.enter.prevent="send" />
        <ul v-if="attachments.length" class="chat-compose__attachments"><li v-for="file in attachments" :key="file.id">{{ file.description || file.uploadName || '已上传附件' }} <button type="button" class="btn btn--ghost btn--sm" :disabled="busy || uploading" @click="attachments = attachments.filter(item => item.id !== file.id)">移除</button></li></ul>
        <p v-if="uploadError" class="chat-thread__error" role="alert">{{ uploadError }}</p>
        <div class="chat-compose__actions">
          <label v-if="!editing" class="btn btn--ghost btn--sm">{{ uploading ? '上传中…' : '添加附件' }}<input class="sr-only" type="file" multiple :disabled="busy || uploading" @change="upload" /></label>
          <button class="btn btn--primary btn--sm" type="submit" :disabled="!sendable">{{ pending === 'send' ? '发送中…' : editing ? '删除并重新发送' : '发送' }}</button>
        </div>
      </form>
    </template>
  </div>
</template>

<style scoped>
.chat-thread .page-header__title { min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; flex: 1; }
.chat-thread__notice, .chat-thread__error { padding: var(--space-3) var(--gutter, var(--space-4)); font-size: var(--text-footnote); overflow-wrap: anywhere; }
.chat-thread__notice { color: var(--color-label-secondary); background: var(--color-fill); }
.chat-thread__error { color: var(--color-danger); }
.chat-thread__older, .chat-thread__empty { text-align: center; padding: var(--space-4); color: var(--color-label-secondary); }
.chat-members { padding: var(--space-4); border-bottom: 1px solid var(--color-separator); }
.chat-members h2 { font-size: var(--text-title3); }
.chat-members p, .chat-members small { color: var(--color-label-secondary); font-size: var(--text-footnote); }
.chat-members ul { list-style: none; padding: 0; }
.chat-members li { display: flex; gap: var(--space-2); align-items: center; padding-block: var(--space-2); }
.chat-members li > span { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.chat-compose { padding: var(--space-4); border-top: 1px solid var(--color-separator); }
.chat-compose textarea { display: block; box-sizing: border-box; width: 100%; min-width: 0; resize: vertical; border: 1px solid var(--color-separator); border-radius: var(--radius-sm); background: var(--color-fill); color: var(--color-label); padding: var(--space-3); font: inherit; }
.chat-compose__context { font-size: var(--text-footnote); color: var(--color-label-secondary); overflow-wrap: anywhere; }
.chat-compose__actions { display: flex; justify-content: space-between; align-items: center; gap: var(--space-2); margin-top: var(--space-2); }
.chat-compose__attachments { padding-left: var(--space-4); font-size: var(--text-footnote); }
</style>
