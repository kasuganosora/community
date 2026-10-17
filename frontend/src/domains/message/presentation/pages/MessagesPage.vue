<script setup lang="ts">
import { sessionEpoch } from '@/shared/api/sessionScope'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { computed, onMounted, nextTick, ref, watch, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { DialogPlugin, MessagePlugin } from '@/shared/ui/feedback'
import type { MastodonConversation } from '@/shared/types/mastodon'
import { conversationApi } from '../../infrastructure/conversationApi'
import { chatApi } from '../../infrastructure/chatApi'
import { chatAvatars, chatPeerAcct, chatPreview, chatTitle, type ChatConversation, type ChatMemberInput } from '../../domain/chat'
import ChatAvatarStack from '../components/ChatAvatarStack.vue'
import ConversationMembersForm from '../components/ConversationMembersForm.vue'
import { conversationImage, conversationMediaLabel, conversationPreview, conversationTitle, conversationTarget } from '../../domain/conversation'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { isInteractiveClick } from '@/shared/utils/dom'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'

import { t } from '@/shared/i18n'
const LAST_CONVERSATION = 'dm-opened'

const router = useRouter()
const route = useRoute()
const initialRecipient = computed(() => typeof route.query.to === 'string' ? route.query.to.slice(0, 500) : '')
const authStore = useAuthStore()
const feed = usePaginatedList<MastodonConversation>((maxId, limit) => conversationApi.list({ max_id: maxId, limit }), 20)
const { items: conversations, loading, error, hasMore } = feed
const chats = ref<ChatConversation[]>([])
const legacyConversations = computed(() => {
  const linked = new Set(chats.value.map(chat => chat.legacy_conversation_id).filter(Boolean))
  return conversations.value.filter(conversation => !linked.has(conversation.id))
})
const chatLoading = ref(false)
const chatError = ref('')
const chatCursor = ref<string | null>(null)
const creating = ref(!!initialRecipient.value)
const createPending = ref(false)
const createError = ref('')
let createTransaction = crypto.randomUUID()
let createSignature = ''
let version = 0
async function loadChats(more = false) {
  if (chatLoading.value || (more && !chatCursor.value)) return
  const request = version
  chatLoading.value = true
  try {
    const result = await chatApi.list(more ? chatCursor.value! : undefined)
    if (request !== version) return
    chats.value = more ? [...new Map([...chats.value, ...result.conversations].map(chat => [chat.id, chat])).values()] : result.conversations
    chatCursor.value = result.next_cursor
    chatError.value = ''
  } catch { if (request === version) chatError.value = t('message.MessagesPage.s22') }
  finally { if (request === version) chatLoading.value = false }
}
async function create(members: ChatMemberInput[]) {
  if (createPending.value) return
  const signature = JSON.stringify(members)
  if (signature !== createSignature) { createSignature = signature; createTransaction = crypto.randomUUID() }
  const request = version
  createPending.value = true
  createError.value = ''
  try {
    const chat = await chatApi.create(createTransaction, members)
    if (request !== version) return
    creating.value = false
    createTransaction = crypto.randomUUID()
    await router.push(`/messages/${encodeURIComponent(chat.id)}`)
  } catch (e) { if (request === version) createError.value = e instanceof Error ? e.message : t('message.MessagesPage.s23') }
  finally { if (request === version) createPending.value = false }
}
watch(() => sessionEpoch.value, () => {
  version++; chats.value = []; chatCursor.value = null; chatLoading.value = false; chatError.value = ''
  creating.value = false; createPending.value = false; createError.value = ''
  createTransaction = crypto.randomUUID()
}, { flush: 'sync' })
onBeforeUnmount(() => { version++ })


function markLegacyRead(chat: ChatConversation) {
  const item = conversations.value.find(c => c.id === chat.legacy_conversation_id)
  if (!item?.unread) return
  const epoch = sessionEpoch.value
  item.unread = false
  conversationApi.markRead(item.id).catch(() => { if (epoch === sessionEpoch.value) item.unread = true })
}

function avatarOf(c: MastodonConversation) {
  return c.accounts.find(a => a.id !== authStore.currentUser?.id) ?? c.last_status?.account ?? null
}

async function open(c: MastodonConversation, e?: MouseEvent) {
  if (e && isInteractiveClick(e)) return
  const to = conversationTarget(c)
  if (!to) {
    MessagePlugin.warning(t('message.MessagesPage.s24'))
    return
  }
  const epoch = sessionEpoch.value
  const wasUnread = c.unread
  try {
    sessionStorage.setItem(LAST_CONVERSATION, c.id)
  } catch { /* ignore */ }
  try {
    await router.push(to)
  } catch {
    return
  }
  if (!wasUnread || epoch !== sessionEpoch.value) return
  c.unread = false
  conversationApi.markRead(c.id).catch(() => { if (epoch === sessionEpoch.value) c.unread = true })
}

function remove(c: MastodonConversation) {
  const epoch = sessionEpoch.value
  let pending = false
  const dialog = DialogPlugin.confirm({
    header: t('message.MessagesPage.s25'),
    body: t('message.MessagesPage.s26'),
    confirmBtn: { content: t('message.MessagesPage.s27'), theme: 'danger' },
    cancelBtn: t('message.MessagesPage.s28'),
    onConfirm: async () => {
      if (pending) return
      if (epoch !== sessionEpoch.value) { dialog.destroy(); return }
      pending = true
      dialog.setConfirmLoading(true)
      try {
        await conversationApi.remove(c.id)
        if (epoch !== sessionEpoch.value) return
        conversations.value = conversations.value.filter(x => x.id !== c.id)
      } catch (e) {
        if (epoch === sessionEpoch.value) MessagePlugin.error(e instanceof Error ? e.message : t('message.MessagesPage.s29'))
      } finally {
        dialog.destroy()
      }
    },
    onClose: () => dialog.destroy(),
  })
}

onMounted(async () => {
  void loadChats()
  await feed.refresh()
  await nextTick()
  let id = ''
  try { id = sessionStorage.getItem(LAST_CONVERSATION) || '' } catch { /* ignore */ }
  const target = id
    ? document.getElementById(`conversation-${id}`)
    : document.querySelector<HTMLElement>('.conversation--unread')
  target?.scrollIntoView({ block: 'center' })
})
</script>

<template>
  <div class="messages-page">
    <header class="page-header messages-header">
      <h1 class="page-header__title">{{ $t('message.MessagesPage.s1') }}</h1>
      <div class="messages-toolbar">
        <button class="btn btn--primary btn--sm" :disabled="createPending" :aria-expanded="creating" aria-controls="new-message-form" @click="creating = !creating"><t-icon name="edit" :size="18" />{{ $t('message.MessagesPage.s3') }}</button>
      </div>
    </header>
    <p v-if="conversations.some(c => c.unread)" class="messages-unread">{{ $t('message.MessagesPage.s2', [conversations.filter(c => c.unread).length]) }}</p>
    <ConversationMembersForm v-if="creating" id="new-message-form" :key="initialRecipient" :initial-recipient="initialRecipient" :pending="createPending" :error="createError" @submit="create" @cancel="creating = false" />
    <p v-if="chatError" class="messages-banner" role="alert">{{ chatError }} <button class="btn btn--ghost btn--sm" :disabled="chatLoading" @click="loadChats()">{{ chatLoading ? $t('message.MessagesPage.s6') : $t('message.MessagesPage.s7') }}</button></p>

    <p v-if="error && (conversations.length > 0 || chats.length > 0)" class="messages-banner" role="alert">
      {{ error }}
      <button type="button" class="btn btn--ghost btn--sm" @click="feed.refresh">{{ $t('message.MessagesPage.s7') }}</button>
    </p>

    <div v-if="error && conversations.length === 0 && chats.length === 0 && !loading" class="empty-state" role="alert">
      <div class="empty-state__icon"><t-icon name="error-circle" :size="48" /></div>
      <h2 class="empty-state__title">{{ $t('message.MessagesPage.s8') }}</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="feed.refresh">{{ $t('message.MessagesPage.s7') }}</button>
    </div>

    <div v-else-if="!loading && !chatLoading && conversations.length === 0 && chats.length === 0" class="empty-state">
      <div class="empty-state__icon"><t-icon name="mail" :size="48" /></div>
      <h2 class="empty-state__title">{{ $t('message.MessagesPage.s9') }}</h2>
      <p class="empty-state__text">{{ $t('message.MessagesPage.s10') }}</p>
    </div>

    <ul v-else-if="conversations.length > 0 || chats.length > 0" class="conversation-list" :aria-label="$t('message.MessagesPage.s11')">
      <li v-for="chat in chats" :key="`chat-${chat.id}`" class="conversation">
        <router-link class="conversation__chat-link" :to="`/messages/${encodeURIComponent(chat.id)}`" @click="markLegacyRead(chat)">
          <ChatAvatarStack :accounts="chatAvatars(chat, authStore.currentUser?.id)" :size="44" />
          <span class="conversation__body">
            <span class="conversation__head">
              <span class="conversation__title" data-testid="chat-title"><EmojiText :text="chatTitle(chat, authStore.currentUser?.id) || $t('message.MessagesPage.s12')" :emojis="chat.members.flatMap(m => m.account?.emojis ?? [])" /></span>
              <span v-if="chatPeerAcct(chat, authStore.currentUser?.id)" class="conversation__acct">{{ chatPeerAcct(chat, authStore.currentUser?.id) }}</span>
              <RelativeTime v-if="chat.last_message" :datetime="new Date(chat.last_message.created_at).toISOString()" class="conversation__time" />
            </span>
            <span v-if="chatPreview(chat)" class="conversation__preview" data-testid="chat-preview"><span class="conversation__excerpt">{{ chatPreview(chat) }}</span></span>
            <span v-else class="conversation__scope">{{ $t('message.MessagesPage.s13', [chat.members.filter(member => member.active !== false).length]) }}</span>
          </span>
        </router-link>
      </li>
      <li
        v-for="c in legacyConversations"
        :key="c.id"
        :id="`conversation-${c.id}`"
        class="conversation"
        :class="{ 'conversation--unread': c.unread }"
        role="link"
        tabindex="0"
        :aria-label="$t('message.MessagesPage.s15', [conversationTitle(c, authStore.currentUser?.id), c.unread ? $t('message.MessagesPage.s14') : '', conversationPreview(c)])"
        @click="open(c, $event)"
        @keydown.enter.self="open(c)"
        @keydown.space.self.prevent="open(c)"
      >
        <UserAvatar v-if="avatarOf(c)" :account="avatarOf(c)!" :size="44" />
        <div v-else class="conversation__fallback" aria-hidden="true">
          <t-icon name="mail" :size="20" />
        </div>
        <div class="conversation__body">
          <div class="conversation__head">
            <span class="conversation__title"><EmojiText :text="conversationTitle(c, authStore.currentUser?.id)" :emojis="[...c.accounts, ...(c.last_status ? [c.last_status.account] : [])].flatMap(a => a.emojis ?? [])" /></span>
            <RelativeTime v-if="c.last_status" :datetime="c.last_status.created_at" class="conversation__time" />
          </div>
          <p class="conversation__preview">
            <img v-if="conversationImage(c)" class="conversation__thumb" :src="conversationImage(c)!" alt="" />
            <span v-if="conversationMediaLabel(c)" class="conversation__media">{{ conversationMediaLabel(c) }}</span>
            <span class="conversation__excerpt">{{ conversationPreview(c) || (conversationMediaLabel(c) ? '' : $t('message.MessagesPage.s16')) }}</span>
          </p>
        </div>
        <span v-if="c.unread" class="conversation__dot">{{ $t('message.MessagesPage.s18') }}</span>
        <button type="button" class="conversation__remove" :aria-label="$t('message.MessagesPage.s19')" @click.stop="remove(c)">
          <t-icon name="close" :size="16" />
        </button>
      </li>
    </ul>

    <div v-if="chatCursor" class="conversation-more"><button class="btn btn--ghost btn--sm" :disabled="chatLoading" @click="loadChats(true)">{{ $t('message.MessagesPage.s20') }}</button></div>
    <div v-if="loading || chatLoading" class="page-loading"><t-loading /></div>
    <div v-else-if="hasMore && conversations.length > 0" class="conversation-more">
      <button type="button" class="btn btn--ghost btn--sm" @click="feed.loadMore">{{ $t('message.MessagesPage.s21') }}</button>
    </div>
  </div>
</template>

<style scoped>
.messages-header { justify-content: space-between; min-height: 72px; border-bottom: 1px solid var(--color-separator); }
.messages-toolbar { margin-left: auto; padding-block: var(--space-2); }
.messages-toolbar button { min-height: var(--hit-target); }
.conversation__chat-link .conversation__body { display: flex; flex-direction: column; gap: var(--space-1); }
.conversation__chat-link .conversation__preview { display: flex; }
.conversation__chat-link .conversation__title { flex: 0 1 auto; }
.conversation__chat-link .conversation__time { margin-left: auto; flex-shrink: 0; }
.conversation__acct { flex-shrink: 1; min-width: 0; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; color: var(--color-label-secondary, var(--color-text-secondary)); font-size: var(--text-footnote, 13px); }
.conversation__chat-link { margin: calc(-1 * var(--space-4)) calc(-1 * var(--gutter, var(--space-4))); padding: var(--space-4) var(--gutter, var(--space-4)); flex: 1; display: flex; align-items: center; gap: var(--space-3); width: 100%; min-width: 0; color: inherit; text-decoration: none; }
.conversation__chat-link .conversation__scope { display: block; }
@media (max-width: 43.74rem) { .conversation__acct { display: none; } }
.messages-page {
  min-height: 100dvh;
}

.messages-unread {
  margin: 0;
  padding: 0 var(--gutter, var(--space-4)) var(--space-2);
  color: var(--color-accent-text);
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
}

.messages-banner {
  display: flex;
  align-items: center;
  justify-content: center;
  flex-wrap: wrap;
  gap: var(--space-2);
  padding: var(--space-3) var(--gutter, var(--space-4));
  color: var(--color-danger, var(--color-label-secondary));
  font-size: var(--text-footnote, var(--font-sm));
}

.conversation-list {
  list-style: none;
  margin: 0;
  padding: 0;
}

.conversation {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  min-height: 88px;
  padding: var(--space-4) var(--gutter, var(--space-4));
  border-bottom: var(--hairline, 1px) solid var(--color-separator, var(--border-color));
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
}

.conversation:active {
  background: var(--color-pressed);
  transition-duration: 0s;
}

.conversation:has(.conversation__remove:active) {
  background: transparent;
}

@media (hover: hover) and (pointer: fine) {
  .conversation:hover {
    background: var(--color-hover);
  }
}

.conversation:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: -2px;
}

.conversation__fallback {
  display: grid;
  place-items: center;
  width: 2.75rem;
  height: 2.75rem;
  flex-shrink: 0;
  border-radius: 50%;
  background: var(--color-fill);
  color: var(--color-label-secondary);
}

.conversation__body {
  flex: 1;
  min-width: 0;
}

.conversation__head {
  display: flex;
  align-items: baseline;
  gap: var(--space-2);
  min-width: 0;
}

.conversation__title {
  flex: 1;
  min-width: 0;
  font-weight: var(--weight-semibold, 600);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.conversation--unread .conversation__title,
.conversation--unread .conversation__preview {
  color: var(--color-label);
  font-weight: var(--weight-semibold, 600);
}

.conversation__time,
.conversation__preview {
  color: var(--color-label-secondary, var(--text-secondary));
  font-size: var(--text-footnote, var(--font-sm));
}

.conversation__time {
  margin-left: auto;
  flex-shrink: 0;
  font-variant-numeric: tabular-nums;
}

.conversation__preview {
  display: flex;
  margin: var(--space-1) 0 0;
  align-items: center;
  gap: var(--space-2);
  min-width: 0;
  overflow: hidden;
  white-space: nowrap;
}

.conversation__excerpt {
  min-width: 0;
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.conversation__thumb {
  width: 1.75rem;
  height: 1.75rem;
  flex-shrink: 0;
  object-fit: cover;
  border-radius: var(--radius-sm, 6px);
  background: var(--color-fill);
}

.conversation__media {
  flex-shrink: 0;
  color: var(--color-label);
  font-weight: var(--weight-medium, 500);
}

.conversation__scope {
  margin: 0;
  color: var(--color-label-tertiary, var(--color-label-secondary));
  font-size: var(--text-caption, 0.75rem);
}

.conversation__dot {
  flex-shrink: 0;
  min-height: 1.25rem;
  padding: 0 var(--space-2);
  border-radius: var(--radius-full, 999px);
  background: var(--color-accent, var(--accent-blue));
  color: #fff;
  font-size: var(--text-caption, 0.75rem);
  font-weight: var(--weight-semibold, 600);
  line-height: 1.25rem;
}

.conversation__remove {
  position: relative;
  display: grid;
  place-items: center;
  width: var(--hit-target);
  height: var(--hit-target);
  flex-shrink: 0;
  border-radius: var(--radius-full, 999px);
  color: var(--color-label-secondary, var(--text-secondary));
  -webkit-tap-highlight-color: transparent;
  transition:
    background-color var(--duration-fast) var(--ease-out),
    color var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-out);
}

@media (hover: hover) and (pointer: fine) {
  .conversation__remove:hover {
    background: var(--color-hover);
    color: var(--color-label);
  }
}

.conversation__remove:active {
  background: var(--color-pressed);
  transform: scale(var(--press-scale-small, 0.92));
  transition-duration: var(--duration-press);
}

.conversation__remove:focus-visible {
  outline: 2px solid var(--color-accent);
  outline-offset: -2px;
}

.conversation-more {
  display: flex;
  justify-content: center;
  padding: var(--space-4);
}
</style>
