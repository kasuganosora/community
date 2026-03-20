<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { MastodonNotification } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import PostCard from '@/shared/components/post/PostCard.vue'

const notifications = ref<MastodonNotification[]>([])
const loading = ref(true)

const error = ref<string | null>(null)

onMounted(async () => {
  try {
    const { data } = await client.get<MastodonNotification[]>('/api/v1/notifications', { params: { limit: 30 } })
    notifications.value = data
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
    case 'mention': return 'chat'
    default: return 'notification'
  }
}

type NotificationType = 'favourite' | 'reblog' | 'follow' | 'mention'

function getNotificationColor(type: NotificationType) {
  switch (type) {
    case 'favourite': return 'var(--accent-pink)'
    case 'reblog': return 'var(--accent-green)'
    case 'follow': return 'var(--accent-blue)'
    case 'mention': return 'var(--accent-blue)'
    default: return 'var(--text-secondary)'
  }
}

function getNotificationText(type: NotificationType) {
  switch (type) {
    case 'favourite': return 'liked your post'
    case 'reblog': return 'reposted your post'
    case 'follow': return 'followed you'
    case 'mention': return 'mentioned you'
    default: return 'interacted'
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

    <template v-else>
      <div 
        v-if="notifications.length > 0" 
        class="notifications-list" 
        role="list" 
        aria-label="Notifications"
      >
        <div class="empty-state__icon">
          <t-icon name="notification" :size="48" />
        </div>
        <h2 class="empty-state__title">Nothing to see here — yet</h2>
        <p class="empty-state__text">
          Likes, reposts, and new followers will show up here.
        </p>
      </div>

      <div
        v-for="notification in notifications"
        :key="notification.id"
        class="notification-item"
        role="listitem"
      >
        <template v-if="notification.type === 'mention' && notification.status">
          <PostCard :status="notification.status" />
        </template>

        <template v-else>
          <div class="notification-body">
            <div class="notification-icon" :style="{ color: getNotificationColor(notification.type as NotificationType) }">
              <t-icon :name="getNotificationIcon(notification.type as NotificationType)" :size="24" />
            </div>
            <div class="notification-content">
              <UserAvatar :account="notification.account" :size="32" />
              <div class="notification-text">
                <strong>{{ notification.account.display_name }}</strong>
                {{ getNotificationText(notification.type as NotificationType) }}
              </div>
              <div v-if="notification.status" class="notification-preview">
                {{ notification.status.content.replace(/<[^>]*>/g, '').slice(0, 100) }}
              </div>
            </div>
          </div>
        </template>
      </div>
    </template>
  </div>
</template>

<style scoped>
.notifications-page {
  min-height: 100vh;
}

.notification-item {
  border-bottom: 1px solid var(--border-color);
}

.notification-body {
  display: flex;
  gap: var(--space-3);
  padding: var(--space-3) var(--space-4);
  cursor: pointer;
  transition: background-color var(--transition-fast);
}

.notification-body:hover {
  background: var(--bg-hover);
}

.notification-icon {
  flex-shrink: 0;
  width: 40px;
  display: flex;
  justify-content: flex-end;
}

.notification-content {
  flex: 1;
  min-width: 0;
}

.notification-text {
  margin-top: var(--space-2);
  font-size: var(--font-md);
  color: var(--text-primary);
}

.notification-preview {
  margin-top: var(--space-1);
  font-size: var(--font-sm);
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
