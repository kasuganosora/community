<script setup lang="ts">
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { onMounted, nextTick } from 'vue'
import { useRouter } from 'vue-router'
import { DialogPlugin, MessagePlugin } from 'tdesign-vue-next'
import type { MastodonConversation } from '@/shared/types/mastodon'
import { conversationApi } from '../../infrastructure/conversationApi'
import { conversationImage, conversationMediaLabel, conversationPreview, conversationTitle, conversationTarget } from '../../domain/conversation'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { isInteractiveClick } from '@/shared/utils/dom'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'

const LAST_CONVERSATION = 'dm-opened'

const router = useRouter()
const authStore = useAuthStore()
const feed = usePaginatedList<MastodonConversation>((maxId, limit) => conversationApi.list({ max_id: maxId, limit }), 20)
const { items: conversations, loading, error, hasMore } = feed

function avatarOf(c: MastodonConversation) {
  return c.accounts.find(a => a.id !== authStore.currentUser?.id) ?? c.last_status?.account ?? null
}

async function open(c: MastodonConversation, e?: MouseEvent) {
  if (e && isInteractiveClick(e)) return
  const to = conversationTarget(c)
  if (!to) {
    MessagePlugin.warning('这条会话没有可打开的消息')
    return
  }
  const wasUnread = c.unread
  try {
    sessionStorage.setItem(LAST_CONVERSATION, c.id)
  } catch { /* ignore */ }
  try {
    await router.push(to)
  } catch {
    return
  }
  if (!wasUnread) return
  c.unread = false
  conversationApi.markRead(c.id).catch(() => { c.unread = true })
}

function remove(c: MastodonConversation) {
  const dialog = DialogPlugin.confirm({
    header: '删除此会话？',
    body: '会话会从列表中移除，已经发出的私信仍保留在对方那里。',
    confirmBtn: { content: '删除', theme: 'danger' },
    cancelBtn: '取消',
    onConfirm: async () => {
      try {
        await conversationApi.remove(c.id)
        conversations.value = conversations.value.filter(x => x.id !== c.id)
      } catch (e) {
        MessagePlugin.error(e instanceof Error ? e.message : '删除失败')
      } finally {
        dialog.destroy()
      }
    },
    onClose: () => dialog.destroy(),
  })
}

function onPosted() {
  MessagePlugin.success('私信已发送')
  feed.refresh()
}

onMounted(async () => {
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
    <div class="page-header">
      <h1 class="page-header__title">私信</h1>
      <p v-if="conversations.some(c => c.unread)" class="messages-unread">{{ conversations.filter(c => c.unread).length }} 个未读会话</p>
    </div>

    <!-- Messages are direct-visibility statuses (Mastodon "private mentions"). -->
    <PostComposer initial-visibility="direct" placeholder="@用户名 写一条私信…" @posted="onPosted" />

    <p v-if="error && conversations.length > 0" class="messages-banner" role="alert">
      {{ error }}
      <button type="button" class="btn btn--ghost btn--sm" @click="feed.refresh">重试</button>
    </p>

    <div v-if="error && conversations.length === 0 && !loading" class="empty-state" role="alert">
      <div class="empty-state__icon"><t-icon name="error-circle" :size="48" /></div>
      <h2 class="empty-state__title">加载失败</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="feed.refresh">重试</button>
    </div>

    <div v-else-if="!loading && conversations.length === 0" class="empty-state">
      <div class="empty-state__icon"><t-icon name="mail" :size="48" /></div>
      <h2 class="empty-state__title">还没有私信</h2>
      <p class="empty-state__text">在上方写给某人。私信只会发给被 @ 的人。</p>
    </div>

    <ul v-else-if="conversations.length > 0" class="conversation-list" aria-label="私信会话">
      <li
        v-for="c in conversations"
        :key="c.id"
        :id="`conversation-${c.id}`"
        class="conversation"
        :class="{ 'conversation--unread': c.unread }"
        role="link"
        tabindex="0"
        :aria-label="`${conversationTitle(c, authStore.currentUser?.id)}${c.unread ? '，未读' : ''}，仅提及的人可见，${conversationPreview(c)}`"
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
            <span class="conversation__excerpt">{{ conversationPreview(c) || (conversationMediaLabel(c) ? '' : '（没有可显示的消息）') }}</span>
          </p>
          <p class="conversation__scope">仅提及的人可见</p>
        </div>
        <span v-if="c.unread" class="conversation__dot">未读</span>
        <button type="button" class="conversation__remove" aria-label="删除此会话" @click.stop="remove(c)">
          <t-icon name="close" :size="16" />
        </button>
      </li>
    </ul>

    <div v-if="loading" class="page-loading"><t-loading /></div>
    <div v-else-if="hasMore && conversations.length > 0" class="conversation-more">
      <button type="button" class="btn btn--ghost btn--sm" @click="feed.loadMore">加载更多</button>
    </div>
  </div>
</template>

<style scoped>
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
  min-height: var(--hit-target);
  padding: var(--space-2) var(--space-2) var(--space-2) var(--gutter, var(--space-4));
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
