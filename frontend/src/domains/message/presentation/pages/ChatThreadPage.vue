<script setup lang="ts">
import { useUnsavedWork } from '@/shared/composables/useUnsavedWork'
import { documentNearEnd, isMobileLayout, scrollDocumentToEnd, useDockedComposer } from '@/shared/composables/useMobileDock'
import { displayAccount } from '@/shared/utils/accountDisplay'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { DialogPlugin } from '@/shared/ui/feedback'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { MAX_MEDIA_ATTACHMENTS } from '@/domains/post/domain/compose'
import { useMediaDrop, acceptMedia } from '@/shared/composables/useMediaDrop'
import type { MastodonAttachment } from '@/shared/types/mastodon'
import { chatAvatars, chatPeerAcct, chatTitle, type ChatMember, type ChatMessage, type ChatMemberInput } from '../../domain/chat'
import ChatAvatarStack from '../components/ChatAvatarStack.vue'
import { useChatThread } from '../composables/useChatThread'
import ChatMessageView from '../components/ChatMessageView.vue'
import ConversationMembersForm from '../components/ConversationMembersForm.vue'

import EmojiText from '@/shared/components/common/EmojiText.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { stripHtml } from '@/shared/utils/sanitize'
import { messagePreview } from '../../domain/messagePreview'
import { t } from '@/shared/i18n'
const route = useRoute()
const router = useRouter()
const auth = useAuthStore()
const id = () => String(route.params.conversationId || '')
const thread = useChatThread(id)
const { conversation, messages, cursor, loading, loadingOlder, refreshPending, pending, busy, error } = thread
const composer = ref<HTMLFormElement>()
useDockedComposer(composer)
const draft = ref('')
const reply = ref<ChatMessage | undefined>()
const editing = ref<ChatMessage | undefined>()
const attachments = ref<(MastodonAttachment & { uploadName?: string })[]>([])
interface PendingUpload { key: string; file: File; name: string; failed: boolean }
const pendingUploads = ref<PendingUpload[]>([])
const uploading = computed(() => pendingUploads.value.some(item => !item.failed))
const uploadError = ref('')
const membersOpen = ref(false)
const history = computed(() => {
  const byId = new Map(messages.value.map(message => [message.event_id, message]))
  return messages.value.map((message, index) => {
    const date = new Date(message.created_at)
    const previous = messages.value[index - 1]
    const member = conversation.value?.members.find(item => item.matrix_id === message.sender || item.acct === message.sender)
    return { message, reply: byId.get(message.reply_event_id || ''), account: message.own ? auth.currentUser ?? undefined : member?.account,
      date: !previous || new Date(previous.created_at).toDateString() !== date.toDateString() ? date.toLocaleDateString(undefined, { year: 'numeric', month: 'long', day: 'numeric' }) : '' }
  })
})
const replySummary = computed(() => {
  if (!reply.value) return ''
  const current = messages.value.find(message => message.event_id === reply.value?.event_id)
  if (!current) return t('message.ChatMessageView.s2')
  if (current.redacted) return t('message.ChatMessageView.s1')
  if (current.status?.spoiler_text) return current.status.spoiler_text
  if (current.status?.sensitive) return t('message.chat.attachment')
  return messagePreview(current.status ? stripHtml(current.status.content) : current.body)
})
const inviting = ref(false)
let uploadGeneration = 0
let transactionId = crypto.randomUUID()
onBeforeUnmount(() => { uploadGeneration++ })
const sendable = computed(() => (!!draft.value.trim() || attachments.value.length > 0) && !busy.value && pendingUploads.value.length === 0)
// Unsent work survives a deploy auto-reload (see useVersionSync).
useUnsavedWork<{ text: string; attachments: typeof attachments.value }>(() => id() ? `dm:${id()}` : null, {
  busy: () => busy.value || !!draft.value.trim() || attachments.value.length > 0 || pendingUploads.value.length > 0,
  snapshot: () => (draft.value.trim() || attachments.value.length) && !editing.value ? { text: draft.value, attachments: attachments.value } : null,
  restore: (saved) => {
    if (typeof saved.text === 'string') draft.value = saved.text
    if (Array.isArray(saved.attachments)) attachments.value = saved.attachments.slice(0, MAX_MEDIA_ATTACHMENTS)
  },
})
function keepInputFocus(event: PointerEvent) {
  // Blurring a phone textarea changes the keyboard/tab-bar dock offset. Keep
  // toolbar targets still between pointerdown and click; keyboard focus is unaffected.
  if (isMobileLayout() && document.activeElement === composer.value?.querySelector('textarea') &&
    event.target instanceof Element && event.target.closest('button, label')) event.preventDefault()
}
function onSendKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || (!event.ctrlKey && !event.metaKey) || event.isComposing) return
  event.preventDefault()
  void send()
}
watch([draft, () => attachments.value.map(file => file.id).join(','), () => reply.value?.event_id, () => editing.value?.event_id], () => { transactionId = crypto.randomUUID() }, { flush: 'sync' })
watch([id, () => sessionEpoch.value], () => {
  uploadGeneration++; draft.value = ''; attachments.value = []; reply.value = undefined; editing.value = undefined
  pendingUploads.value = []; uploadError.value = ''; membersOpen.value = false; inviting.value = false
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
function upload(event: Event) {
  const input = event.target as HTMLInputElement
  const files = Array.from(input.files || [])
  input.value = ''
  addFiles(files)
}
/** Picker, paste and drop share one path, bounded by the attachment limit. */
function addFiles(files: File[]) {
  if (!files.length || busy.value || editing.value) return
  const room = MAX_MEDIA_ATTACHMENTS - attachments.value.length - pendingUploads.value.length
  uploadError.value = files.length > room ? t('message.ChatThreadPage.tooManyAttachments', [MAX_MEDIA_ATTACHMENTS]) : ''
  for (const file of files.slice(0, Math.max(0, room))) {
    const item: PendingUpload = { key: crypto.randomUUID(), file, name: file.name || t('message.ChatThreadPage.s22'), failed: false }
    pendingUploads.value = [...pendingUploads.value, item]
    void uploadOne(item)
  }
}
async function uploadOne(item: PendingUpload) {
  const version = uploadGeneration
  setPending(item.key, { failed: false })
  try {
    const media = await postApi.uploadMedia(item.file)
    if (version !== uploadGeneration) return
    attachments.value.push({ ...media, uploadName: item.name })
    pendingUploads.value = pendingUploads.value.filter(p => p.key !== item.key)
  } catch {
    if (version !== uploadGeneration) return
    setPending(item.key, { failed: true })
    uploadError.value = t('message.ChatThreadPage.s28')
  }
}
function setPending(key: string, patch: Partial<PendingUpload>) {
  pendingUploads.value = pendingUploads.value.map(p => p.key === key ? { ...p, ...patch } : p)
}
function retryUpload(item: PendingUpload) { uploadError.value = ''; void uploadOne(item) }
function dropPending(item: PendingUpload) { pendingUploads.value = pendingUploads.value.filter(p => p.key !== item.key) }
const { dragging: composeDragging } = useMediaDrop(composer, { onFiles: addFiles, enabled: () => !busy.value && !editing.value, accept: acceptMedia })
watch(() => messages.value.at(-1)?.event_id, async (next, previous) => {
  if (!next) return
  const nearEnd = !previous || documentNearEnd(180)
  const room = id(), epoch = sessionEpoch.value
  await nextTick()
  if (nearEnd && room === id() && epoch === sessionEpoch.value) { if (isMobileLayout()) scrollDocumentToEnd(); else composer.value?.scrollIntoView?.({ block: 'end' }) }
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
  let confirming = false
  const dialog = DialogPlugin.confirm({ header: t('message.ChatThreadPage.s29'), body: t('message.ChatThreadPage.s30'), confirmBtn: t('message.ChatThreadPage.s31'), cancelBtn: t('message.ChatThreadPage.s32'),
    onConfirm: async () => { if (confirming) return; confirming = true; dialog.setConfirmLoading(true); if (room === id() && epoch === sessionEpoch.value) await thread.remove(message); dialog.destroy() }, onClose: () => dialog.destroy(),
  })
}
function removeMember(member: ChatMember) {
  const self = member.account_id === auth.currentUser?.id
  const room = id(), epoch = sessionEpoch.value
  let confirming = false
  const dialog = DialogPlugin.confirm({ header: self ? t('message.ChatThreadPage.s33') : t('message.ChatThreadPage.s34'), body: t('message.ChatThreadPage.s35'), confirmBtn: self ? t('message.ChatThreadPage.s10') : t('message.ChatThreadPage.s11'), cancelBtn: t('message.ChatThreadPage.s32'),
    onConfirm: async () => { if (confirming) return; confirming = true; dialog.setConfirmLoading(true); if (room === id() && epoch === sessionEpoch.value && await thread.removeMember(member.id) && self) await router.push('/messages'); dialog.destroy() }, onClose: () => dialog.destroy(),
  })
}
async function invite(members: ChatMemberInput[]) { if (await thread.addMembers(members)) inviting.value = false }
</script>

<template>
  <div class="chat-thread">
    <header class="page-header page-header--with-back chat-thread__header">
      <router-link class="page-header__back-btn" to="/messages" :aria-label="$t('message.ChatThreadPage.s1')"><t-icon name="chevron-left" :size="20" /></router-link>
      <ChatAvatarStack v-if="conversation" :accounts="chatAvatars(conversation, auth.currentUser?.id)" :size="36" />
      <h1 class="page-header__title" data-testid="chat-thread-title">
        <EmojiText :text="(conversation && chatTitle(conversation, auth.currentUser?.id)) || $t('message.ChatThreadPage.s2')" :emojis="conversation?.members.flatMap(m => m.account?.emojis ?? []) ?? []" />
        <span v-if="conversation && chatPeerAcct(conversation, auth.currentUser?.id)" class="chat-thread__acct">{{ chatPeerAcct(conversation, auth.currentUser?.id) }}</span>
      </h1>
      <button v-if="conversation" class="btn btn--ghost btn--sm" :aria-expanded="membersOpen" @click="membersOpen = !membersOpen">{{ $t('message.ChatThreadPage.s3') }}</button>
    </header>
    <p class="chat-thread__notice"><t-icon name="lock-off" :size="14" /><span>{{ $t('message.ChatThreadPage.s4') }}</span></p>
    <p v-if="error && !conversation" class="chat-thread__error" role="alert">{{ error }} <button class="btn btn--ghost btn--sm" :disabled="loading || loadingOlder || refreshPending || busy" :aria-busy="refreshPending" @click="thread.reload">{{ refreshPending || loading ? $t('message.ChatThreadPage.s5') : $t('message.ChatThreadPage.s6') }}</button></p>
    <div v-if="loading" class="page-loading"><t-loading /></div>
    <template v-if="conversation">
      <section v-if="membersOpen" class="chat-members" :aria-label="$t('message.ChatThreadPage.s7')">
        <h2>{{ $t('message.ChatThreadPage.s3') }}</h2>
        <p>{{ $t('message.ChatThreadPage.s8') }}</p>
        <ul>
          <li v-for="member in conversation.members.filter(item => item.active !== false)" :key="member.id">
            <UserAvatar v-if="member.account" :account="member.account" :size="32" />
            <span>{{ member.display_name || displayAccount(member.acct || member.matrix_id) || $t('message.ChatThreadPage.s9', [member.account_id]) }} <small v-if="member.acct">@{{ displayAccount(member.acct) }} · </small><small>{{ member.kind === 'matrix' ? 'Matrix' : member.kind === 'ap' ? 'ActivityPub' : 'Community' }}</small></span>
            <button v-if="member.account_id === auth.currentUser?.id ? conversation.can_leave : conversation.can_manage" class="btn btn--ghost btn--sm" :disabled="busy" @click="removeMember(member)">{{ member.account_id === auth.currentUser?.id ? $t('message.ChatThreadPage.s10') : $t('message.ChatThreadPage.s11') }}</button>
          </li>
        </ul>
        <button v-if="conversation.can_manage && !inviting" class="btn btn--secondary btn--sm" @click="inviting = true">{{ $t('message.ChatThreadPage.s12') }}</button>
        <ConversationMembersForm v-if="inviting" mode="invite" :pending="busy" :error="error" @submit="invite" @cancel="inviting = false" />
      </section>
      <div v-if="cursor" class="chat-thread__older"><button class="btn btn--ghost btn--sm" :disabled="loadingOlder || refreshPending || busy" @click="thread.older">{{ loadingOlder ? $t('message.ChatThreadPage.s5') : $t('message.ChatThreadPage.s13') }}</button></div>
      <p v-if="!messages.length" class="chat-thread__empty">{{ $t('message.ChatThreadPage.s14') }}</p>
      <section class="chat-thread__history" :aria-label="$t('message.ChatThreadPage.s15')">
        <template v-for="item in history" :key="item.message.event_id">
          <p v-if="item.date" class="chat-thread__date">{{ item.date }}</p>
          <ChatMessageView :message="item.message" :account="item.account" :pending="busy || uploading" :reply="item.reply" @reply="replyTo(item.message)" @edit="edit(item.message)" @remove="remove(item.message)" @retry="thread.retry(item.message)" @refresh-delivery="thread.delivery(item.message)" />
        </template>
      </section>
      <form ref="composer" class="chat-compose" :class="{ 'chat-compose--dropping': composeDragging }" :aria-busy="pending === 'send'" @pointerdown="keepInputFocus" @submit.prevent="send">
        <div class="chat-compose__box">
          <p v-if="error" class="chat-thread__error" role="alert">{{ error }} <button type="button" class="btn btn--ghost btn--sm" :disabled="loadingOlder || refreshPending || busy" :aria-busy="refreshPending" @click="thread.reload">{{ refreshPending ? $t('message.ChatThreadPage.s5') : $t('message.ChatThreadPage.s6') }}</button></p>
          <p v-if="editing" class="chat-compose__context" role="note">{{ $t('message.ChatThreadPage.s16') }}<button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="editing = undefined; draft = ''">{{ $t('message.ChatThreadPage.s17') }}</button></p>
          <p v-else-if="reply" class="chat-compose__context"><t-icon name="rollback" :size="16" /><span>{{ $t('message.ChatThreadPage.s18', [replySummary]) }}</span> <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="reply = undefined" :aria-label="$t('message.ChatThreadPage.s19')"><t-icon name="close" :size="16" /></button></p>
          <label class="sr-only" for="chat-message-input">{{ $t('message.ChatThreadPage.s20') }}</label>
          <textarea id="chat-message-input" v-model="draft" rows="2" :placeholder="$t('message.ChatThreadPage.s21')" :disabled="busy" @keydown="onSendKeydown" />
          <ul v-if="attachments.length" class="chat-compose__attachments">
            <li v-for="file in attachments" :key="file.id">
            <img v-if="file.type === 'image' && (file.preview_url || file.url)" :src="file.preview_url || file.url" alt="" />
            <t-icon v-else name="file" :size="22" />
            <span>{{ file.description || file.uploadName || $t('message.ChatThreadPage.s22') }}</span>
            <button type="button" class="btn btn--ghost btn--sm" :disabled="busy || uploading" :aria-label="$t('message.ConversationMembersForm.s9', [file.uploadName || file.description || $t('message.ChatThreadPage.s22')])" @click="attachments = attachments.filter(item => item.id !== file.id)"><t-icon name="close" :size="16" /></button>
            </li>
          </ul>
          <ul v-if="pendingUploads.length" class="chat-compose__attachments" :aria-label="$t('message.ChatThreadPage.s23')">
            <li v-for="item in pendingUploads" :key="item.key" :data-state="item.failed ? 'failed' : 'uploading'">
              <t-loading v-if="!item.failed" size="small" />
              <t-icon v-else name="error-circle" :size="20" />
              <span>{{ item.name }}</span>
              <button v-if="item.failed" type="button" class="btn btn--ghost btn--sm" data-testid="chat-upload-retry" :disabled="busy" @click="retryUpload(item)">{{ $t('message.ChatThreadPage.retryUpload') }}</button>
              <button v-if="item.failed" type="button" class="btn btn--ghost btn--sm" :aria-label="$t('message.ConversationMembersForm.s9', [item.name])" @click="dropPending(item)"><t-icon name="close" :size="16" /></button>
            </li>
          </ul>
          <p v-if="uploadError" class="chat-thread__error" role="alert">{{ uploadError }}</p>
          <div class="chat-compose__actions">
            <label v-if="!editing" class="btn btn--ghost btn--sm chat-compose__attach" :class="{ 'is-disabled': busy || uploading }"><t-icon name="file-add" :size="20" /><span>{{ uploading ? $t('message.ChatThreadPage.s23') : $t('message.ChatThreadPage.s24') }}</span><input class="sr-only" type="file" multiple :disabled="busy || uploading" @change="upload" /></label>
            <button class="btn btn--primary btn--sm chat-compose__send" type="submit" :disabled="!sendable"><t-icon name="send" :size="18" />{{ pending === 'send' ? $t('message.ChatThreadPage.s25') : editing ? $t('message.ChatThreadPage.s26') : $t('message.ChatThreadPage.s27') }}</button>
          </div>
        </div>
      </form>
    </template>
  </div>
</template>

<style scoped>
.chat-thread { display: flex; flex-direction: column; min-height: 100dvh; }
.chat-thread__header { gap: var(--space-3); min-height: 72px; border-bottom: 1px solid var(--color-separator); }
.chat-thread__acct { display: block; font-size: var(--text-footnote); font-weight: normal; color: var(--color-label-secondary); margin-top: 2px; overflow: hidden; text-overflow: ellipsis; }
.chat-thread .page-header__title { min-width: 0; flex: 1; font-size: var(--text-headline); line-height: 1.4; }
.chat-thread__notice, .chat-thread__error { padding: var(--space-3) var(--gutter); margin: 0; font-size: var(--text-footnote); overflow-wrap: anywhere; }
.chat-thread__notice { display: flex; align-items: baseline; justify-content: center; gap: var(--space-2); color: var(--color-label-secondary); font-size: var(--text-caption); }
.chat-thread__notice :deep(svg) { flex-shrink: 0; }
.chat-thread__error { color: var(--color-danger); }
.chat-thread__older, .chat-thread__empty { text-align: center; padding: var(--space-4); color: var(--color-label-secondary); }
.chat-thread__history { flex: 1; width: 100%; max-width: 900px; min-width: 0; margin-inline: auto; padding: 0 var(--gutter) var(--space-4); }
.chat-thread__date { display: flex; align-items: center; justify-content: center; gap: var(--space-4); margin: var(--space-4) 0 var(--space-5); color: var(--color-label-secondary); font-size: var(--text-caption); }
.chat-thread__date::before, .chat-thread__date::after { content: ''; width: 40px; height: 1px; background: var(--color-separator); }
.chat-members { margin: var(--space-3) var(--gutter); padding: var(--space-4); border: 1px solid var(--color-separator); border-radius: var(--radius-lg); background: var(--color-bg-grouped); }
.chat-members h2 { margin: 0 0 var(--space-2); font-size: var(--text-title3); }
.chat-members p, .chat-members small { color: var(--color-label-secondary); font-size: var(--text-footnote); }
.chat-members ul { list-style: none; padding: 0; }
.chat-members li { display: flex; gap: var(--space-2); align-items: center; padding-block: var(--space-2); }
.chat-members li > span { flex: 1; min-width: 0; overflow-wrap: anywhere; }
.chat-compose { position: sticky; bottom: 0; z-index: 5; padding: var(--space-3) var(--gutter) var(--space-4); background: var(--color-bg); }
.chat-compose__box { max-width: 852px; margin-inline: auto; border: 1px solid var(--color-separator); border-radius: 18px; background: var(--color-bg-grouped); padding: var(--space-3); }
.chat-compose__box:has(textarea:focus-visible) { border-color: var(--color-accent); box-shadow: 0 0 0 2px var(--color-accent-soft); }
.chat-compose textarea { display: block; box-sizing: border-box; width: 100%; min-width: 0; min-height: 3rem; max-height: min(220px, 30dvh); field-sizing: content; resize: none; overflow-y: auto; border: 0; border-radius: 0; background: transparent; color: var(--color-label); padding: var(--space-1); font: inherit; font-size: 1rem; line-height: var(--leading-body); }
.chat-compose textarea:focus-visible { outline: none; box-shadow: none; }
.chat-compose__context { display: flex; align-items: center; gap: var(--space-2); margin: 0 0 var(--space-2); padding: var(--space-2); background: var(--color-fill); border-radius: var(--radius-sm); border-left: 2px solid var(--color-accent); font-size: var(--text-footnote); color: var(--color-label-secondary); overflow-wrap: anywhere; }
.chat-compose__context > span { flex: 1; display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; }
.chat-compose__context > button, .chat-compose__context :deep(svg) { flex-shrink: 0; }
.chat-compose__actions { display: flex; justify-content: space-between; align-items: center; gap: var(--space-2); margin-top: var(--space-2); }
.chat-compose__actions > :is(button,label) { min-height: var(--hit-target); }
.chat-compose--dropping .chat-compose__box { outline: 2px dashed var(--color-accent); outline-offset: 2px; }
.chat-compose__attach { color: var(--color-label-secondary); cursor: pointer; }
.chat-compose__attach.is-disabled { opacity: .5; cursor: default; }
.chat-compose__actions > label:has(input:focus-visible) { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.chat-compose__send { margin-left: auto; border-radius: 12px; }
.chat-compose__attachments { display: flex; flex-wrap: wrap; gap: var(--space-2); list-style: none; padding: 0; margin: var(--space-2) 0; max-height: 160px; overflow: auto; font-size: var(--text-footnote); }
.chat-compose__attachments li { display: flex; align-items: center; gap: var(--space-2); min-width: 0; max-width: 100%; padding-left: var(--space-2); background: var(--color-fill); border-radius: var(--radius-sm); }
.chat-compose__attachments li > span { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.chat-compose__attachments img { width: 36px; height: 36px; border-radius: 4px; object-fit: cover; }
.chat-compose__attachments button { flex-shrink: 0; }
.chat-compose .chat-thread__error { padding: var(--space-2); }
@media (forced-colors: active) {
  .chat-compose__box:has(textarea:focus-visible) { outline: 2px solid Highlight; }
}
@media (max-width: 43.74rem) {
  .chat-thread__header { gap: var(--space-2); min-height: 64px; }
  .chat-thread__history { padding-inline: var(--space-3); }
  .chat-compose { position: fixed; left: 0; right: 0; bottom: var(--dock-bottom); z-index: var(--z-fab, 20); padding: var(--space-2); }
  .chat-compose__box { max-height: calc(100dvh - var(--dock-bottom) - 80px); overflow-y: auto; }
  .chat-compose textarea { min-height: 2.75rem; max-height: 9rem; }
  .chat-thread { min-height: calc(100dvh - var(--tabbar-h, 0px)); padding-bottom: var(--composer-h, 0px); }
  .chat-compose__attachments { max-height: 100px; }
}
</style>
