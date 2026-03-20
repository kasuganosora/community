<script setup lang="ts">
import { ref, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { postApi } from '../../infrastructure/postApi'
import PostCard from '@/shared/components/post/PostCard.vue'
import RichText from '@/shared/components/common/RichText.vue'

const route = useRoute()
const router = useRouter()

const status = ref<MastodonStatus | null>(null)
const ancestors = ref<MastodonStatus[]>([])
const descendants = ref<MastodonStatus[]>([])
const loading = ref(true)

onMounted(async () => {
  const statusId = route.params.statusId as string
  try {
    const [statusData, contextData] = await Promise.all([
      postApi.getStatus(statusId),
      postApi.getContext(statusId),
    ])
    status.value = statusData
    ancestors.value = contextData.ancestors
    descendants.value = contextData.descendants
  } catch {
    // Status not found or error
  } finally {
    loading.value = false
  }
})

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

    <template v-else-if="status">
      <!-- Ancestors (context above) -->
      <PostCard v-for="s in ancestors" :key="s.id" :status="s" />

      <!-- Main post (highlighted) -->
      <article class="detail-main">
        <div class="detail-main__header">
          <t-avatar :size="'48px'" :image="status.account.avatar || undefined" shape="circle">
            {{ status.account.display_name?.charAt(0) || 'U' }}
          </t-avatar>
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
      <PostCard v-for="s in descendants" :key="s.id" :status="s" />
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
