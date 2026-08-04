<script setup lang="ts">
import { ref, onMounted } from 'vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { bookmarkApi } from '../../infrastructure/bookmarkApi'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import PostCard from '@/shared/components/post/PostCard.vue'

const bookmarks = ref<MastodonStatus[]>([])
const loading = ref(true)
const error = ref<string | null>(null)

// Reuse the shared action handlers — optimistic update on the local array.
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(bookmarks.value, id, updates),
)

onMounted(async () => {
  try {
    bookmarks.value = await bookmarkApi.list({ limit: 40 })
  } catch (e) {
    console.error('Failed to load bookmarks:', e)
    error.value = 'Failed to load bookmarks'
  } finally {
    loading.value = false
  }
})
</script>


<template>
  <div class="bookmarks-page">
    <div class="page-header">
      <h1 class="page-header__title">Bookmarks</h1>
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

    <template v-else-if="bookmarks.length">
      <PostCard
        v-for="s in bookmarks"
        :key="s.id"
        :status="s"
        @reblog="toggleReblog"
        @favourite="toggleFavourite"
        @bookmark="toggleBookmark"
      />
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
