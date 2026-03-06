<script setup lang="ts">
import { ref } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import PostCard from '@/shared/components/post/PostCard.vue'

const bookmarks = ref<MastodonStatus[]>([])
const loading = ref(false)

// TODO: Fetch from GET /api/v1/bookmarks
</script>

<template>
  <div class="bookmarks-page">
    <div class="page-header">
      <h1 class="page-header__title">Bookmarks</h1>
    </div>

    <div v-if="loading" class="page-loading">
      <t-loading />
    </div>

    <template v-else-if="bookmarks.length">
      <PostCard v-for="s in bookmarks" :key="s.id" :status="s" />
    </template>

    <div v-else class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="bookmark" :size="48" />
      </div>
      <h2 class="empty-state__title">Save posts for later</h2>
      <p class="empty-state__text">
        Bookmark posts to easily find them again in the future.
      </p>
    </div>
  </div>
</template>

<style scoped>
.bookmarks-page {
  min-height: 100vh;
}
</style>
