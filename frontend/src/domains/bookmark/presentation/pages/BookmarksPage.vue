<script setup lang="ts">
import { onMounted } from 'vue'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { bookmarkApi } from '../../infrastructure/bookmarkApi'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import PostCard from '@/shared/components/post/PostCard.vue'

const feed = usePaginatedList((maxId, limit) => bookmarkApi.list({ max_id: maxId, limit }), 40)
const { items: bookmarks, loading, error, hasMore } = feed
const load = feed.refresh

// Reuse the shared action handlers — optimistic update on the local array.
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(bookmarks.value, id, updates),
)

onMounted(load)
</script>


<template>
  <div class="bookmarks-page">
    <div class="page-header">
      <h1 class="page-header__title">{{ $t('bookmark.BookmarksPage.s1') }}</h1>
    </div>

    <div v-if="loading && !bookmarks.length" class="page-loading">
      <t-loading />
    </div>

    <div v-else-if="error && !bookmarks.length" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('bookmark.BookmarksPage.s2') }}</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="load">{{ $t('bookmark.BookmarksPage.s3') }}</button>
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
      <p v-if="error" role="alert">{{ error }}</p>
      <button :disabled="loading" v-if="hasMore" type="button" class="btn btn--secondary btn--sm" @click="feed.loadMore">{{ $t('bookmark.BookmarksPage.s4') }}</button>
    </template>

    <div v-else class="empty-state">
      <div class="empty-state__icon">
        <t-icon name="bookmark" :size="48" />
      </div>
      <h2 class="empty-state__title">{{ $t('bookmark.BookmarksPage.s5') }}</h2>
      <p class="empty-state__text">
        {{ $t('bookmark.BookmarksPage.s6') }}
      </p>
    </div>
  </div>
</template>

<style scoped>
.bookmarks-page {
  min-height: 100dvh;
}
</style>
