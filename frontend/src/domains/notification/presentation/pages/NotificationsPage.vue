<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { MastodonNotification } from '@/shared/types/mastodon'
import client from '@/shared/api/client'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import PostCard from '@/shared/components/post/PostCard.vue'

const notifications = ref<MastodonNotification[]>([])
const loading = ref(true)

onMounted(async () => {
  try {
    const { data } = await client.get<MastodonNotification[]>('/api/v1/notifications', { params: { limit: 30 } })
    notifications.value = data
  } catch {
    // Silently fail
  } finally {
    loading.value = false
  }
})

function getNotificationIcon(type: string) {
  switch (type) {
    case 'favourite': return 'heart-filled'
    case 'reblog': return 'swap'
    case 'follow': return 'user-add'
    case 'mention': return 'chat'
    default: return 'notification'
  }
}

function getNotificationColor(type: string) {
  switch (type) {
    case 'favourite': return 'var(--accent-pink)'
    case 'reblog': return 'var(--accent-green)'
    case 'follow': return 'var(--accent-blue)'
    default: return 'var(--text-secondary)'
  }
}

function getNotificationText(type: string) {
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

    <template v-else>
      <div v-if="notifications.length === 0" class="empty-state">
        <div class="empty-state__icon">
          <t-icon name="notification" :size="48" />
        </div>
        <h2 class="empty-state__title">Nothing to see here — yet</h2>
        <p class="empty-state__text">
          Likes, reposts, and new followers will show up here.
        </p>
      </div>

      <div
        v-for="notif in notifications"
        :key="notif.id"
        class="notification-item"
      >
        <template v-if="notif.type === 'mention' && notif.status">
          <PostCard :status="notif.status" />
        </template>

        <template v-else>
          <div class="notification-body">
            <div class="notification-icon" :style="{ color: getNotificationColor(notif.type) }">
              <t-icon :name="getNotificationIcon(notif.type)" :size="24" />
            </div>
            <div class="notification-content">
              <UserAvatar :account="notif.account" :size="32" />
              <div class="notification-text">
                <strong>{{ notif.account.display_name }}</strong>
                {{ getNotificationText(notif.type) }}
              </div>
              <div v-if="notif.status" class="notification-preview">
                {{ notif.status.content.replace(/<[^>]*>/g, '').slice(0, 100) }}
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
  gap: 12px;
  padding: 12px 16px;
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
  margin-top: 8px;
  font-size: var(--font-md);
  color: var(--text-primary);
}

.notification-preview {
  margin-top: 4px;
  font-size: var(--font-sm);
  color: var(--text-secondary);
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}
</style>
