<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { postApi } from '../../infrastructure/postApi'
import { useStatusActions, applyStatusUpdate } from '../../presentation/composables/useStatusActions'
import PostCard from '@/shared/components/post/PostCard.vue'
import RichText from '@/shared/components/common/RichText.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

const route = useRoute()
const router = useRouter()

const status = ref<MastodonStatus | null>(null)
const ancestors = ref<MastodonStatus[]>([])
const descendants = ref<MastodonStatus[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

// All status arrays (ancestors + descendants + main status) need updates;
// the helper applies to whichever array contains the matching id.
const allLists = () => [ancestors.value, descendants.value]
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions((id, updates) => {
  for (const list of allLists()) applyStatusUpdate(list, id, updates)
  if (status.value?.id === id) {
    Object.assign(status.value, updates)
  } else if (status.value?.reblog?.id === id) {
    Object.assign(status.value.reblog, updates)
  }
})

// Version guard — prevents a stale response (from navigating between
// posts quickly) from overwriting newer post data.
let requestVersion = 0

async function loadPost() {
  const myVersion = ++requestVersion
  const statusId = route.params.statusId as string
  loading.value = true
  error.value = null
  try {
    const [statusData, contextData] = await Promise.all([
      postApi.getStatus(statusId),
      postApi.getContext(statusId),
    ])
    if (myVersion !== requestVersion) return
    status.value = statusData
    ancestors.value = contextData.ancestors
    descendants.value = contextData.descendants
  } catch (e) {
    if (myVersion !== requestVersion) return
    console.error('Failed to load post:', e)
    error.value = 'Failed to load this post'
    status.value = null
  } finally {
    if (myVersion === requestVersion) {
      loading.value = false
    }
  }
}

onMounted(loadPost)
watch(() => route.params.statusId, loadPost)

function goBack() {
  // Check if there's history to go back to, otherwise go to home
  if (window.history.length > 1) {
    router.back()
  } else {
    router.push({ name: 'home' })
  }
}
</script>

<template>
  <div class="detail-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" @click="goBack" aria-label="Go back">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title">Post</h1>
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

    <template v-else-if="status">
      <!-- Ancestors (context above) -->
      <PostCard
        v-for="s in ancestors"
        :key="s.id"
        :status="s"
        @reblog="toggleReblog"
        @favourite="toggleFavourite"
        @bookmark="toggleBookmark"
      />

      <!-- Main post (highlighted) -->
      <article class="detail-main">
        <div class="detail-main__header">
          <UserAvatar :account="status.account" :size="48" />
          <div class="detail-main__user">
            <div class="detail-main__name">{{ status.account.display_name }}</div>
            <div class="detail-main__username">@{{ status.account.acct }}</div>
          </div>
        </div>

        <RichText class="detail-main__content rich-text" :html="status.content" />

        <div class="detail-main__time">
          <time :datetime="status.created_at">
            {{ new Date(status.created_at).toLocaleString() }}
          </time>
        </div>

        <div class="detail-main__stats">
          <span><strong>{{ status.reblogs_count }}</strong> Reposts</span>
          <span><strong>{{ status.favourites_count }}</strong> Likes</span>
          <span><strong>{{ status.replies_count }}</strong> Replies</span>
        </div>
      </article>

      <!-- Descendants (replies below) -->
      <PostCard
        v-for="s in descendants"
        :key="s.id"
        :status="s"
        @reblog="toggleReblog"
        @favourite="toggleFavourite"
        @bookmark="toggleBookmark"
      />
    </template>
  </div>
</template>

<style scoped>
.detail-page {
  min-height: 100vh;
}

.detail-main {
  padding: var(--space-3) var(--space-4);
  border-bottom: 1px solid var(--border-color);
}

.detail-main__header {
  display: flex;
  align-items: center;
  gap: var(--space-3);
  margin-bottom: var(--space-3);
}

.detail-main__user {
  flex: 1;
}

.detail-main__name {
  font-weight: 700;
  font-size: var(--font-md);
}

.detail-main__username {
  color: var(--text-secondary);
  font-size: var(--font-md);
}

.detail-main__content {
  font-size: var(--font-lg);
  line-height: 1.5;
  margin-bottom: var(--space-4);
}

.detail-main__time {
  color: var(--text-secondary);
  font-size: var(--font-md);
  padding: var(--space-4) 0;
  border-bottom: 1px solid var(--border-color);
}

.detail-main__stats {
  display: flex;
  gap: var(--space-5);
  padding: var(--space-4) 0;
  border-bottom: 1px solid var(--border-color);
  color: var(--text-secondary);
  font-size: var(--font-sm);
}

.detail-main__stats strong {
  color: var(--text-primary);
}
</style>
