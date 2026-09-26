<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { MastodonNotification, NotificationType } from '@/shared/types/mastodon'
import { notificationApi } from '../../infrastructure/notificationApi'
import { stripHtml } from '@/shared/utils/sanitize'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import PostCard from '@/shared/components/post/PostCard.vue'

const notifications = ref<MastodonNotification[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

// Reuse the shared action handlers — optimistic update on the notification
// statuses (mention notifications carry a status that renders a PostCard).
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions((id, updates) => {
  for (const n of notifications.value) {
    if (n.status) applyStatusUpdate([n.status], id, updates)
  }
})

onMounted(async () => {
  try {
    notifications.value = await notificationApi.list({ limit: 30 })
  } catch (e) {
    console.error('Failed to load notifications:', e)
    error.value = 'Failed to load notifications'
  } finally {
    loading.value = false
  }
})

function getNotificationIcon(type: NotificationType) {
  switch (type) {
    case 'favourite': return 'heart-filled'
    case 'reblog': return 'swap'
    case 'follow': return 'user-add'
    case 'follow_request': return 'user-add'
    case 'mention': return 'chat'
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
    default: return 'var(--text-secondary)'
  }
}

function getNotificationText(type: NotificationType) {
  switch (type) {
    case 'favourite': return 'liked your post'
    case 'reblog': return 'reposted your post'
    case 'follow': return 'followed you'
    case 'follow_request': return 'requested to follow you'
    case 'mention': return 'mentioned you'
    case 'poll': return 'a poll has ended'
    case 'update': return 'edited a post'
    case 'status': return 'posted a new status'
    default: return 'interacted'
  }
}

function getPreview(content: string): string {
  return stripHtml(content).slice(0, 100)
}

async function dismissNotification(id: string) {
  try {
    await notificationApi.dismiss(id)
    notifications.value = notifications.value.filter(n => n.id !== id)
  } catch (e) {
    console.error('Failed to dismiss notification:', e)
  }
}

</script>


<template>
  <div class="notifications-page">
    <div class="page-header">
      <h1 class="page-header__title">Notifications</h1>
    </div>

    <div v-if="loading" class="page-loading">
      <t-loading />
    </div>

    <div v-else-if="error" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">Something went wrong</h2>
      <p class="empty-state__text">{{ error }}</p>
    </div>

    <div v-else-if="notifications.length === 0" class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="notification" :size="48" />
      </div>
      <h2 class="empty-state__title">Nothing to see here — yet</h2>
      <p class="empty-state__text">
        Likes, reposts, and new followers will show up here.
      </p>
    </div>

    <div v-else class="notifications-list" role="list" aria-label="Notifications">
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
          <div class="notification-body">
            <div class="notification-icon" :style="{ color: getNotificationColor(notification.type) }">
              <t-icon :name="getNotificationIcon(notification.type)" :size="24" />
            </div>
            <div class="notification-content">
              <UserAvatar :account="notification.account" :size="32" />
              <div class="notification-text">
                <strong>{{ notification.account.display_name }}</strong>
                {{ getNotificationText(notification.type) }}
              </div>
              <div v-if="notification.status" class="notification-preview">
                {{ getPreview(notification.status.content) }}
              </div>
            </div>
            <button
              class="notification-dismiss"
              @click.stop="dismissNotification(notification.id)"
              aria-label="Dismiss notification"
            >
              <t-icon name="close" :size="16" />
            </button>
          </div>
        </template>
      </div>
    </div>
  </div>
</template>

<style scoped>
.notifications-page {
  min-height: 100dvh;
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
