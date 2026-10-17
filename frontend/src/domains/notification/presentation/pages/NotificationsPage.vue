<script setup lang="ts">
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import ReactionLabel from '@/shared/components/post/ReactionLabel.vue'
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { ref, watch, onMounted, onUnmounted } from 'vue'
import { useRouter } from 'vue-router'
import { MessagePlugin } from '@/shared/ui/feedback'
import type { MastodonNotification, NotificationType } from '@/shared/types/mastodon'
import { notificationApi } from '../../infrastructure/notificationApi'
import { notificationTarget, notificationText, notificationQuery, type NotificationFilter } from '../../domain/notification'
import { stripHtml } from '@/shared/utils/sanitize'
import { isInteractiveClick } from '@/shared/utils/dom'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'
import PostCard from '@/shared/components/post/PostCard.vue'
import TabBar from '@/shared/components/common/TabBar.vue'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { useNotificationUnread } from '@/shared/composables/useNotificationUnread'

import { t } from '@/shared/i18n'
const PAGE_SIZE = 30
const FILTERS: { value: NotificationFilter; label: string }[] = [
  { value: 'all', label: t('notification.NotificationsPage.s17') },
  { value: 'mention', label: t('notification.NotificationsPage.s18') },
]

const router = useRouter()
const authStore = useAuthStore()
// The poll author gets "your poll has ended"; voters "a poll you took part in".
function isOwnPoll(n: { type: string, status?: { account?: { id: string } } | null }): boolean {
  return n.type === 'poll' && !!n.status?.account && n.status.account.id === authStore.currentUser?.id
}
const filter = ref<NotificationFilter>('all')
const feed = usePaginatedList<MastodonNotification>(
  (maxId, limit) => notificationApi.list({ max_id: maxId, limit, ...notificationQuery(filter.value) }),
  PAGE_SIZE,
)
const { items: notifications, loading, error, hasMore } = feed
const { markRead } = useNotificationUnread()
// The newest loaded notification is what the user has now seen.
watch(() => (filter.value === 'all' ? notifications.value[0]?.id : undefined), (id) => { if (id) void markRead(id) })
// New notifications (any type, e.g. a closed poll) appear without reloading.
useFeedStream(ref('stream=notification'), async () => {
  const ok = await feed.revalidate()
  if (feed.newer.value.length) await feed.revealNewer()
  return ok
})

// Mention notifications render a PostCard; keep its actions optimistic.
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions((id, updates) => {
  for (const n of notifications.value) {
    if (n.status) applyStatusUpdate([n.status], id, updates)
  }
})

function getNotificationIcon(type: NotificationType) {
  switch (type) {
    case 'emoji_reaction': return 'chat'
    case 'favourite': return 'heart-filled'
    case 'reblog': return 'swap'
    case 'follow': return 'user-add'
    case 'follow_request': return 'user-add'
    case 'mention': return 'chat'
    case 'poll': return 'chart-bar'
    case 'update': return 'edit'
    default: return 'notification'
  }
}

function getNotificationColor(type: NotificationType) {
  switch (type) {
    case 'favourite': return 'var(--accent-pink)'
    case 'reblog': return 'var(--accent-green)'
    case 'follow': return 'var(--accent-blue)'
    case 'follow_request': return 'var(--accent-blue)'
    case 'mention': return 'var(--accent-blue)'
    case 'poll': return 'var(--color-accent-text)'
    default: return 'var(--text-secondary)'
  }
}

function getPreview(content: string): string {
  return stripHtml(content).slice(0, 100)
}

function open(n: MastodonNotification, e?: MouseEvent) {
  if (e && isInteractiveClick(e)) return
  router.push(notificationTarget(n)).catch(() => {})
}

async function dismissNotification(id: string) {
  try {
    await notificationApi.dismiss(id)
    notifications.value = notifications.value.filter(n => n.id !== id)
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : t('notification.NotificationsPage.s19'))
  }
}

const confirmClear = ref(false)
const clearing = ref(false)
let alive = true
async function clearAll() {
  if (clearing.value) return
  const epoch = sessionEpoch.value
  const current = () => alive && epoch === sessionEpoch.value
  clearing.value = true
  try {
    await notificationApi.clear()
    if (!current()) return
    feed.reset()
    confirmClear.value = false
    MessagePlugin.success(t('notification.NotificationsPage.s20'))
  } catch (e) {
    if (current()) MessagePlugin.error(e instanceof Error ? e.message : t('notification.NotificationsPage.s21'))
  } finally {
    if (current()) clearing.value = false
  }
}
watch(sessionEpoch, () => { clearing.value = false; confirmClear.value = false }, { flush: 'sync' })

// Infinite scroll
const sentinel = ref<HTMLElement>()
let observer: IntersectionObserver | null = null
onMounted(() => {
  feed.refresh()
  observer = new IntersectionObserver((entries) => {
    if (entries[0]?.isIntersecting && !error.value) feed.loadMore()
  }, { rootMargin: '200px' })
  if (sentinel.value) observer.observe(sentinel.value)
})
onUnmounted(() => { alive = false; observer?.disconnect() })
watch(filter, () => {
  feed.reset()
  void feed.refresh()
}, { flush: 'sync' })
</script>

<template>
  <div class="notifications-page">
    <div class="page-header notifications-header">
      <h1 class="page-header__title">{{ $t('notification.NotificationsPage.s1') }}</h1>
      <button
        v-if="notifications.length > 0 && !confirmClear"
        type="button"
        class="btn btn--ghost btn--sm"
        @click="confirmClear = true"
      >
{{ $t('notification.NotificationsPage.s2') }}
</button>
      <span v-if="confirmClear" class="notifications-confirm">
        <span>{{ $t('notification.NotificationsPage.s3') }}</span>
        <button type="button" class="btn btn--ghost btn--sm" :disabled="clearing" @click="confirmClear = false">{{ $t('notification.NotificationsPage.s4') }}</button>
        <button type="button" class="btn btn--danger btn--sm" :disabled="clearing" :aria-busy="clearing" @click="clearAll">{{ clearing ? $t('notification.NotificationsPage.s5') : $t('notification.NotificationsPage.s2') }}</button>
      </span>
    </div>

    <TabBar v-model="filter" :options="FILTERS" :label="$t('notification.NotificationsPage.s6')" />

    <div v-if="error && notifications.length === 0" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('notification.NotificationsPage.s7') }}</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="feed.refresh">{{ $t('notification.NotificationsPage.s8') }}</button>
    </div>

    <div v-else-if="!loading && notifications.length === 0" class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="notification" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('notification.NotificationsPage.s9') }}</h2>
      <p class="empty-state__text">
        {{ filter === 'mention' ? $t('notification.NotificationsPage.s10') : $t('notification.NotificationsPage.s11') }}
      </p>
    </div>

    <div v-else class="notifications-list" role="list" :aria-label="$t('notification.NotificationsPage.s1')">
      <div
        v-for="notification in notifications"
        :key="notification.id"
        class="notification-item"
        role="listitem"
      >
        <template v-if="notification.type === 'mention' && notification.status">
          <PostCard
            :status="notification.status"
            @reblog="toggleReblog"
            @favourite="toggleFavourite"
            @bookmark="toggleBookmark"
          />
        </template>

        <template v-else>
          <div
            class="notification-body"
            role="link"
            tabindex="0"
            :aria-label="`${notification.account.display_name || notification.account.username} ${notificationText(notification.type, isOwnPoll(notification))}`"
            @click="open(notification, $event)"
            @keydown.enter.self="open(notification)"
          >
            <div class="notification-icon" :style="{ color: getNotificationColor(notification.type) }">
              <t-icon :name="getNotificationIcon(notification.type)" :size="24" />
            </div>
            <div class="notification-content">
              <UserAvatar :account="notification.account" :size="32" />
              <div class="notification-text">
                <strong><EmojiText :text="notification.account.display_name || notification.account.username" :emojis="notification.account.emojis" /></strong>
                <template v-if="notification.type === 'emoji_reaction' && notification.emoji_reaction">{{ $t('notification.NotificationsPage.s12') }} <ReactionLabel :reaction="notification.emoji_reaction" /> {{ $t('notification.NotificationsPage.s13') }}</template>
                <template v-else>{{ notificationText(notification.type, isOwnPoll(notification)) }}</template>
                <RelativeTime :datetime="notification.created_at" class="notification-time" />
              </div>
              <div v-if="notification.status" class="notification-preview">
                <EmojiText
                  :text="notification.status.spoiler_text ? $t('notification.NotificationsPage.s14', [notification.status.spoiler_text]) : getPreview(notification.status.content)"
                  :emojis="notification.status.emojis"
                />
              </div>
            </div>
            <button
              type="button"
              class="notification-dismiss"
              @click.stop="dismissNotification(notification.id)"
              :aria-label="$t('notification.NotificationsPage.s15')"
            >
              <t-icon name="close" :size="16" />
            </button>
          </div>
        </template>
      </div>
    </div>

    <div ref="sentinel" class="notifications-sentinel" />
    <div v-if="loading" class="page-loading"><t-loading /></div>
    <p v-else-if="error && notifications.length > 0" class="notifications-more-error" role="alert">
      {{ error }} <button type="button" class="btn btn--ghost btn--sm" @click="feed.loadMore">{{ $t('notification.NotificationsPage.s8') }}</button>
    </p>
    <p v-else-if="!hasMore && notifications.length > 0" class="notifications-end">{{ $t('notification.NotificationsPage.s16') }}</p>
  </div>
</template>

<style scoped>
.notifications-page {
  min-height: 100dvh;
}

.notifications-header {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-2);
}

.notifications-confirm {
  display: flex;
  align-items: center;
  gap: var(--space-2);
  font-size: var(--font-sm);
}

.notification-time {
  margin-left: var(--space-1);
  color: var(--text-secondary);
  font-size: var(--font-sm);
}

.notifications-sentinel {
  height: 1px;
}

.notifications-end,
.notifications-more-error {
  padding: var(--space-4);
  text-align: center;
  font-size: var(--font-sm);
  color: var(--text-secondary);
}

.notification-item {
  position: relative;
}

/* Inset hairline aligned with the content column, like PostCard. */
.notification-item + .notification-item::before {
  content: '';
  position: absolute;
  top: 0;
  left: calc(var(--gutter) + 2.75rem + var(--space-3));
  right: 0;
  height: var(--hairline);
  background: var(--color-separator);
  z-index: 1;
}

.notification-item :deep(.post-card::after) {
  display: none;
}

.notification-body {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--gutter);
  cursor: pointer;
  -webkit-tap-highlight-color: transparent;
  transition: background-color var(--duration-release) var(--ease-out);
}

@media (hover: hover) and (pointer: fine) {
  .notification-body:hover {
    background: var(--color-hover);
  }
}

.notification-body:active {
  background: var(--color-pressed);
  transition-duration: 0s;
}

/* Type glyph in the avatar column: the only colour on the row. */
.notification-icon {
  flex-shrink: 0;
  width: 2.75rem;
  display: flex;
  justify-content: flex-end;
  padding-top: var(--space-0);
}

.notification-icon :deep(svg) {
  width: 1.625rem;
  height: 1.625rem;
}

.notification-content {
  flex: 1;
  min-width: 0;
}

.notification-text {
  margin-top: var(--space-2);
  font-size: var(--text-body);
  line-height: var(--leading-body);
  color: var(--color-label-secondary);
}

.notification-text strong {
  margin-inline-end: 0.25em;
  color: var(--color-label);
  font-weight: var(--weight-semibold);
}

.notification-preview {
  margin-top: var(--space-1);
  font-size: var(--text-subhead);
  line-height: var(--leading-subhead);
  color: var(--color-label-secondary);
  display: -webkit-box;
  -webkit-line-clamp: 2;
  -webkit-box-orient: vertical;
  overflow: hidden;
}

.notification-dismiss {
  flex-shrink: 0;
  align-self: flex-start;
  display: grid;
  place-items: center;
  width: 2rem;
  height: 2rem;
  margin: calc(-1 * var(--space-1)) calc(-1 * var(--space-2)) 0 0;
  border-radius: var(--radius-full);
  color: var(--color-label-tertiary);
  transition:
    background-color var(--duration-fast) var(--ease-out),
    color var(--duration-fast) var(--ease-out),
    transform var(--duration-release) var(--ease-out);
}

/* Enlarge the hit area to 44pt without growing the glyph. */
.notification-dismiss::after {
  content: '';
  position: absolute;
  inset: -6px;
}

.notification-dismiss {
  position: relative;
}

@media (hover: hover) and (pointer: fine) {
  .notification-dismiss:hover {
    background: var(--color-hover);
    color: var(--color-label);
  }
}

.notification-dismiss:active {
  background: var(--color-pressed);
  transform: scale(var(--press-scale-small));
  transition-duration: var(--duration-press);
}
</style>
