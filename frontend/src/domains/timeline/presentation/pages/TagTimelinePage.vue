<script setup lang="ts">
import { computed, ref, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import { timelineApi } from '../../infrastructure/timelineApi'
import { tagApi } from '../../infrastructure/tagApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import TimelineFeed from '../components/TimelineFeed.vue'

const route = useRoute()
const authStore = useAuthStore()
const tag = computed(() => String(route.params.tag ?? '').replace(/^#/, ''))
const following = ref(false)
const followBusy = ref(false)

async function loadFollow() {
  if (!authStore.currentUser || !tag.value) {
    following.value = false
    return
  }
  try {
    const info = await tagApi.get(tag.value)
    following.value = !!info.following
  } catch {
    following.value = false
  }
}

async function toggleFollow() {
  if (!tag.value || followBusy.value) return
  followBusy.value = true
  try {
    const info = following.value ? await tagApi.unfollow(tag.value) : await tagApi.follow(tag.value)
    following.value = !!info.following
    MessagePlugin.success(following.value ? '已关注话题' : '已取消关注')
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? e.message : '操作失败')
  } finally {
    followBusy.value = false
  }
}

const feed = usePaginatedList(
  (maxId, limit) => timelineApi.getTagTimeline(tag.value, { max_id: maxId, limit }),
  TIMELINE_PAGE_SIZE,
)
const { items: statuses, loading, error, hasMore } = feed

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

watch(tag, () => { feed.reset(); feed.refresh(); void loadFollow() })
onMounted(() => { feed.refresh(); void loadFollow() })
</script>

<template>
  <div class="tag-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" type="button" aria-label="返回" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title">#{{ tag }}</h1>
      <button v-if="authStore.currentUser" type="button" class="btn btn--secondary btn--sm tag-follow" :disabled="followBusy" @click="toggleFollow">
        {{ following ? '已关注话题' : '关注话题' }}
      </button>
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
  min-height: 100dvh;
}
.tag-follow { margin-left: auto; flex-shrink: 0; }
</style>
