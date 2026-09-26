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

// The backend pages bookmarks by bookmark-row id and sends no Link header,
// so the last status id is not a usable cursor: load one large page.
async function load() {
  loading.value = true
  error.value = null
  try {
    bookmarks.value = await bookmarkApi.list({ limit: 40 })
  } catch (e) {
    console.error('Failed to load bookmarks:', e)
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    loading.value = false
  }
}

onMounted(load)
</script>


<template>
  <div class="bookmarks-page">
    <div class="page-header">
      <h1 class="page-header__title">收藏</h1>
    </div>

    <div v-if="loading" class="page-loading">
      <t-loading />
    </div>

    <div v-else-if="error" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">加载失败</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="load">重试</button>
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
      <h2 class="empty-state__title">还没有收藏</h2>
      <p class="empty-state__text">
        点击帖子上的收藏按钮，之后可以在这里找到它们。
      </p>
    </div>
  </div>
</template>

<style scoped>
.bookmarks-page {
  min-height: 100dvh;
}
</style>
