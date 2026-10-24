<script setup lang="ts">
import NewPostsPill from '@/shared/components/common/NewPostsPill.vue'
import { computed, ref, watch, onMounted } from 'vue'
import { useRoute } from 'vue-router'
import { MessagePlugin } from '@/shared/ui/feedback'
import { timelineApi } from '../../infrastructure/timelineApi'
import { tagApi } from '../../infrastructure/tagApi'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import TimelineFeed from '../components/TimelineFeed.vue'

import { t } from '@/shared/i18n'
const route = useRoute()
const authStore = useAuthStore()
const tag = computed(() => String(route.params.tag ?? '').replace(/^#/, ''))
const following = ref(false)
const followBusy = ref(false)
// The server's spelling of the tag (#Ukraine for /tags/ukraine).
const canonicalName = ref('')
const displayTag = computed(() =>
  canonicalName.value && canonicalName.value.toLowerCase() === tag.value.toLowerCase() ? canonicalName.value : tag.value,
)

async function loadFollow() {
  const requested = tag.value
  const epoch = sessionEpoch.value
  canonicalName.value = ''
  following.value = false
  if (!requested) return
  try {
    const info = await tagApi.get(requested)
    if (requested !== tag.value || epoch !== sessionEpoch.value) return
    canonicalName.value = info.name || ''
    following.value = !!authStore.currentUser && !!info.following
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
    MessagePlugin.success(following.value ? t('timeline.TagTimelinePage.s2') : t('timeline.TagTimelinePage.s6'))
  } catch (e) {
    MessagePlugin.error(e instanceof Error && e.message ? e.message : t('timeline.TagTimelinePage.s7'))
  } finally {
    followBusy.value = false
  }
}

const feed = usePaginatedList(
  (maxId, limit) => timelineApi.getTagTimeline(tag.value, { max_id: maxId, limit }),
  TIMELINE_PAGE_SIZE,
  8,
)
const { items: statuses, loading, error, hasMore, newer, hasPrevious } = feed
const streamTarget = computed(() => `stream=hashtag&tag=${encodeURIComponent(tag.value)}`)
useFeedStream(streamTarget, feed.revalidate)
async function reloadHead() { await feed.refresh(); if (!error.value) window.scrollTo(0, 0) }

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

watch(sessionEpoch, () => { following.value = false; canonicalName.value = '' }, { flush: 'sync' })
watch(tag, () => { feed.reset(); feed.refresh(); void loadFollow() })
onMounted(() => { feed.refresh(); void loadFollow() })
</script>

<template>
  <div class="tag-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" type="button" :aria-label="$t('timeline.TagTimelinePage.s1')" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title">#{{ displayTag }}</h1>
      <button v-if="authStore.currentUser" type="button" class="btn btn--secondary btn--sm tag-follow" :disabled="followBusy" @click="toggleFollow">
        {{ following ? $t('timeline.TagTimelinePage.s2') : $t('timeline.TagTimelinePage.s3') }}
      </button>
    </div>
    <NewPostsPill :show="newer.length > 0" :reveal="reloadHead" />
    <TimelineFeed
      :statuses="statuses"
      :scope-key="streamTarget"
      :has-previous="hasPrevious"
      @reload-head="reloadHead"
      @load-previous="feed.loadPrevious"
      @pinned-ids="feed.setPinnedIds"
      :loading="loading"
      :has-more="hasMore"
      :error="error"
      :empty-title="$t('timeline.TagTimelinePage.s4')"
      :empty-text="$t('timeline.TagTimelinePage.s5', [displayTag])"
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
