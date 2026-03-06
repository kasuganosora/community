<script setup lang="ts">
import { computed } from 'vue'
import { useRouter } from 'vue-router'
import type { MastodonStatus } from '@/shared/types/mastodon'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import RelativeTime from '@/shared/components/common/RelativeTime.vue'
import RichText from '@/shared/components/common/RichText.vue'
import PostMedia from './PostMedia.vue'
import PostActions from './PostActions.vue'

const props = defineProps<{
  status: MastodonStatus
}>()

const emit = defineEmits<{
  reblog: [status: MastodonStatus]
  favourite: [status: MastodonStatus]
  bookmark: [status: MastodonStatus]
}>()

const router = useRouter()

const displayStatus = computed(() => props.status.reblog || props.status)
const isReblog = computed(() => !!props.status.reblog)

function navigateToDetail() {
  const s = displayStatus.value
  router.push({
    name: 'post-detail',
    params: { username: s.account.username, statusId: s.id },
  })
}

function navigateToProfile(e: Event) {
  e.stopPropagation()
  router.push({
    name: 'profile',
    params: { username: displayStatus.value.account.acct },
  })
}
</script>

<template>
  <article class="post-card" @click="navigateToDetail">
    <!-- Reblog header -->
    <div v-if="isReblog" class="post-card__reblog">
      <t-icon name="swap" :size="14" />
      <span>{{ status.account.display_name }} reposted</span>
    </div>

    <div class="post-card__body">
      <!-- Avatar -->
      <div class="post-card__avatar" @click="navigateToProfile">
        <UserAvatar :account="displayStatus.account" :size="40" />
      </div>

      <!-- Content -->
      <div class="post-card__content">
        <!-- Header -->
        <div class="post-card__header">
          <span class="post-card__name" @click="navigateToProfile">
            {{ displayStatus.account.display_name }}
          </span>
          <span class="post-card__username">
            @{{ displayStatus.account.acct }}
          </span>
          <span class="post-card__dot">&middot;</span>
          <RelativeTime :datetime="displayStatus.created_at" />
        </div>

        <!-- Text -->
        <RichText :html="displayStatus.content" class="post-card__text" />

        <!-- Media -->
        <PostMedia
          v-if="displayStatus.media_attachments.length"
          :attachments="displayStatus.media_attachments"
        />

        <!-- Actions -->
        <PostActions
          :status="displayStatus"
          @reply="navigateToDetail"
          @reblog="emit('reblog', displayStatus)"
          @favourite="emit('favourite', displayStatus)"
          @bookmark="emit('bookmark', displayStatus)"
        />
      </div>
    </div>
  </article>
</template>

<style scoped>
.post-card {
  padding: 12px 16px;
  border-bottom: 1px solid var(--border-color);
  cursor: pointer;
  transition: background-color var(--transition-fast);
}

.post-card:hover {
  background: var(--bg-hover);
}

.post-card__reblog {
  display: flex;
  align-items: center;
  gap: 4px;
  color: var(--text-secondary);
  font-size: var(--font-xs);
  font-weight: 700;
  padding-left: 52px;
  margin-bottom: 4px;
}

.post-card__body {
  display: flex;
  gap: 12px;
}

.post-card__avatar {
  flex-shrink: 0;
}

.post-card__content {
  flex: 1;
  min-width: 0;
}

.post-card__header {
  display: flex;
  align-items: center;
  gap: 4px;
  flex-wrap: nowrap;
  overflow: hidden;
}

.post-card__name {
  font-weight: 700;
  font-size: var(--font-md);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  color: var(--text-primary);
}

.post-card__name:hover {
  text-decoration: underline;
}

.post-card__username {
  color: var(--text-secondary);
  font-size: var(--font-md);
  white-space: nowrap;
  overflow: hidden;
  text-overflow: ellipsis;
  flex-shrink: 1;
}

.post-card__dot {
  color: var(--text-secondary);
  flex-shrink: 0;
}

.post-card__text {
  margin-top: 4px;
  font-size: var(--font-md);
  color: var(--text-primary);
}
</style>
