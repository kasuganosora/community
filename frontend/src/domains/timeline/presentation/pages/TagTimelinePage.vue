<script setup lang="ts">
import { computed, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { timelineApi } from '../../infrastructure/timelineApi'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import TimelineFeed from '../components/TimelineFeed.vue'

const route = useRoute()
const tag = computed(() => String(route.params.tag ?? '').replace(/^#/, ''))

const feed = usePaginatedList(
  (maxId, limit) => timelineApi.getTagTimeline(tag.value, { max_id: maxId, limit }),
  TIMELINE_PAGE_SIZE,
)
const { items: statuses, loading, error, hasMore } = feed

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

watch(tag, () => { feed.reset(); feed.refresh() })
onMounted(() => feed.refresh())
</script>

<template>
  <div class="tag-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" type="button" aria-label="返回" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title">#{{ tag }}</h1>
    </div>
    <TimelineFeed
      :statuses="statuses"
      :loading="loading"
      :has-more="hasMore"
      :error="error"
      empty-title="暂无帖子"
      :empty-text="`还没有带 #${tag} 的公开帖子。`"
      @load-more="feed.loadMore"
      @retry="statuses.length && hasMore ? feed.loadMore() : feed.refresh()"
      @reblog="toggleReblog"
      @favourite="toggleFavourite"
      @bookmark="toggleBookmark"
    />
  </div>
</template>

<style scoped>
.tag-page {
  min-height: 100vh;
}
</style>
