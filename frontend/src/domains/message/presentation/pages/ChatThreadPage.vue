<script setup lang="ts">
import { documentNearEnd, isMobileLayout, scrollDocumentToEnd, useDockedComposer } from '@/shared/composables/useMobileDock'
import { displayAccount } from '@/shared/utils/accountDisplay'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { computed, nextTick, onBeforeUnmount, ref, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { DialogPlugin } from '@/shared/ui/feedback'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { postApi } from '@/domains/post/infrastructure/postApi'
import type { MastodonAttachment } from '@/shared/types/mastodon'
import { chatAvatars, chatPeerAcct, chatTitle, type ChatMember, type ChatMessage, type ChatMemberInput } from '../../domain/chat'
import ChatAvatarStack from '../components/ChatAvatarStack.vue'
import { useChatThread } from '../composables/useChatThread'
import ChatMessageView from '../components/ChatMessageView.vue'
import ConversationMembersForm from '../components/ConversationMembersForm.vue'

import EmojiText from '@/shared/components/common/EmojiText.vue'
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
const uploading = ref(false)
const uploadError = ref('')
const membersOpen = ref(false)
const inviting = ref(false)
let uploadGeneration = 0
let transactionId = crypto.randomUUID()
onBeforeUnmount(() => { uploadGeneration++ })
const sendable = computed(() => (!!draft.value.trim() || attachments.value.length > 0) && !busy.value && !uploading.value)
function onSendKeydown(event: KeyboardEvent) {
  if (event.key !== 'Enter' || (!event.ctrlKey && !event.metaKey) || event.isComposing) return
  event.preventDefault()
  void send()
}
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
  } catch { if (version === uploadGeneration) uploadError.value = t('message.ChatThreadPage.s28') }
  finally { if (version === uploadGeneration) uploading.value = false }
}
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
    <header class="page-header page-header--with-back">
      <router-link class="page-header__back-btn" to="/messages" :aria-label="$t('message.ChatThreadPage.s1')"><t-icon name="chevron-left" :size="20" /></router-link>
      <ChatAvatarStack v-if="conversation" :accounts="chatAvatars(conversation, auth.currentUser?.id)" :size="32" />
      <h1 class="page-header__title" data-testid="chat-thread-title">
        <EmojiText :text="(conversation && chatTitle(conversation, auth.currentUser?.id)) || $t('message.ChatThreadPage.s2')" :emojis="conversation?.members.flatMap(m => m.account?.emojis ?? []) ?? []" />
        <span v-if="conversation && chatPeerAcct(conversation, auth.currentUser?.id)" class="chat-thread__acct">{{ chatPeerAcct(conversation, auth.currentUser?.id) }}</span>
      </h1>
      <button v-if="conversation" class="btn btn--ghost btn--sm" :aria-expanded="membersOpen" @click="membersOpen = !membersOpen">{{ $t('message.ChatThreadPage.s3') }}</button>
    </header>
    <p class="chat-thread__notice">{{ $t('message.ChatThreadPage.s4') }}</p>
    <p v-if="error" class="chat-thread__error" role="alert">{{ error }} <button class="btn btn--ghost btn--sm" :disabled="loading || loadingOlder || refreshPending || busy" :aria-busy="refreshPending" @click="thread.reload">{{ refreshPending || loading ? $t('message.ChatThreadPage.s5') : $t('message.ChatThreadPage.s6') }}</button></p>
    <div v-if="loading" class="page-loading"><t-loading /></div>
    <template v-if="conversation">
      <section v-if="membersOpen" class="chat-members" :aria-label="$t('message.ChatThreadPage.s7')">
        <h2>{{ $t('message.ChatThreadPage.s3') }}</h2>
        <p>{{ $t('message.ChatThreadPage.s8') }}</p>
        <ul>
<li v-for="member in conversation.members.filter(item => item.active !== false)" :key="member.id">
          <span>{{ member.display_name || displayAccount(member.acct || member.matrix_id) || $t('message.ChatThreadPage.s9', [member.account_id]) }} <small v-if="member.acct">@{{ displayAccount(member.acct) }} · </small><small>{{ member.kind === 'matrix' ? 'Matrix' : member.kind === 'ap' ? 'ActivityPub' : 'Community' }}</small></span>
          <button v-if="member.account_id === auth.currentUser?.id ? conversation.can_leave : conversation.can_manage" class="btn btn--ghost btn--sm" :disabled="busy" @click="removeMember(member)">{{ member.account_id === auth.currentUser?.id ? $t('message.ChatThreadPage.s10') : $t('message.ChatThreadPage.s11') }}</button>
        </li>
</ul>
        <button v-if="conversation.can_manage && !inviting" class="btn btn--secondary btn--sm" @click="inviting = true">{{ $t('message.ChatThreadPage.s12') }}</button>
        <ConversationMembersForm v-if="inviting" mode="invite" :pending="busy" :error="error" @submit="invite" @cancel="inviting = false" />
      </section>
      <div v-if="cursor" class="chat-thread__older"><button class="btn btn--ghost btn--sm" :disabled="loadingOlder || refreshPending || busy" @click="thread.older">{{ loadingOlder ? $t('message.ChatThreadPage.s5') : $t('message.ChatThreadPage.s13') }}</button></div>
      <p v-if="!messages.length" class="chat-thread__empty">{{ $t('message.ChatThreadPage.s14') }}</p>
      <div :aria-label="$t('message.ChatThreadPage.s15')">
        <ChatMessageView v-for="message in messages" :key="message.event_id" :message="message" :pending="busy || uploading" :reply="messages.find(item => item.event_id === message.reply_event_id)" @reply="replyTo(message)" @edit="edit(message)" @remove="remove(message)" @retry="thread.retry(message)" @refresh-delivery="thread.delivery(message)" />
      </div>
      <form ref="composer" class="chat-compose" @submit.prevent="send">
        <p v-if="editing" class="chat-compose__context" role="note">{{ $t('message.ChatThreadPage.s16') }}<button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="editing = undefined; draft = ''">{{ $t('message.ChatThreadPage.s17') }}</button></p>
        <p v-else-if="reply" class="chat-compose__context">{{ $t('message.ChatThreadPage.s18', [reply.body]) }} <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click="reply = undefined">{{ $t('message.ChatThreadPage.s19') }}</button></p>
        <label class="sr-only" for="chat-message-input">{{ $t('message.ChatThreadPage.s20') }}</label>
        <textarea id="chat-message-input" v-model="draft" rows="3" :placeholder="$t('message.ChatThreadPage.s21')" :disabled="busy" @keydown="onSendKeydown" />
        <ul v-if="attachments.length" class="chat-compose__attachments"><li v-for="file in attachments" :key="file.id">{{ file.description || file.uploadName || $t('message.ChatThreadPage.s22') }} <button type="button" class="btn btn--ghost btn--sm" :disabled="busy || uploading" @click="attachments = attachments.filter(item => item.id !== file.id)">{{ $t('message.ChatThreadPage.s11') }}</button></li></ul>
        <p v-if="uploadError" class="chat-thread__error" role="alert">{{ uploadError }}</p>
        <div class="chat-compose__actions">
          <label v-if="!editing" class="btn btn--ghost btn--sm">{{ uploading ? $t('message.ChatThreadPage.s23') : $t('message.ChatThreadPage.s24') }}<input class="sr-only" type="file" multiple :disabled="busy || uploading" @change="upload" /></label>
          <button class="btn btn--primary btn--sm" type="submit" :disabled="!sendable">{{ pending === 'send' ? $t('message.ChatThreadPage.s25') : editing ? $t('message.ChatThreadPage.s26') : $t('message.ChatThreadPage.s27') }}</button>
        </div>
      </form>
    </template>
  </div>
</template>

<style scoped>
.chat-thread__acct { margin-left: var(--space-2); font-size: var(--text-footnote, 13px); font-weight: normal; color: var(--color-label-secondary); }
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
.chat-compose textarea { display: block; box-sizing: border-box; width: 100%; min-width: 0; min-height: 5.25rem; max-height: min(220px, 30dvh); field-sizing: content; resize: none; overflow-y: auto; border: 1px solid var(--color-separator); border-radius: var(--radius-md); background: var(--color-bg-grouped); color: var(--color-label); padding: var(--space-3); font: inherit; font-size: 1rem; line-height: var(--leading-body); }
.chat-compose textarea:focus-visible { border-color: var(--color-accent); outline: none; box-shadow: none; }
.chat-compose__context { font-size: var(--text-footnote); color: var(--color-label-secondary); overflow-wrap: anywhere; }
.chat-compose__actions { display: flex; justify-content: space-between; align-items: center; gap: var(--space-2); margin-top: var(--space-2); }
.chat-compose__actions > :is(button,label) { min-height: var(--hit-target); }
.chat-compose__actions > label:has(input:focus-visible) { outline: 2px solid var(--color-accent); outline-offset: 2px; }
.chat-compose__attachments { padding-left: var(--space-4); font-size: var(--text-footnote); }
@media (forced-colors: active) {
  .chat-compose textarea:focus-visible { outline: 2px solid Highlight; outline-offset: 2px; }
}
@media (max-width: 43.74rem) {
  /* Phones: the thread scrolls in the document; the composer docks above the tab bar. */
  .chat-compose { position: fixed; left: 0; right: 0; bottom: var(--dock-bottom); z-index: var(--z-fab, 20); background: var(--color-bg); }
  .chat-compose textarea { min-height: 2.75rem; max-height: 9rem; }
  .chat-thread { padding-bottom: var(--composer-h, 0px); }
}
</style>
