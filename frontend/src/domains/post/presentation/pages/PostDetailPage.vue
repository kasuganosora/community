<script setup lang="ts">
import { ref, computed, onMounted, watch, nextTick } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { postApi } from '../../infrastructure/postApi'
import {
  useStatusActions, applyStatusUpdate, deletedStatusIds, editedStatuses,
} from '../../presentation/composables/useStatusActions'
import PostCard from '@/shared/components/post/PostCard.vue'
import PostContent from '@/shared/components/post/PostContent.vue'
import PostActions from '@/shared/components/post/PostActions.vue'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'

const route = useRoute()
const router = useRouter()

const status = ref<MastodonStatus | null>(null)
const ancestors = ref<MastodonStatus[]>([])
const descendants = ref<MastodonStatus[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const replyComposer = ref<InstanceType<typeof PostComposer>>()

// The status actually shown: the reblogged original if the id is a reblog,
// with any edit made in this session applied.
const mainStatus = computed(() => {
  const base = status.value?.reblog ?? status.value
  if (!base) return null
  const edited = editedStatuses.get(base.id)
  return edited ? { ...base, ...edited } : base
})

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
    const code = (e as { response?: { status?: number } })?.response?.status
    error.value = code === 404 ? '这条帖子不存在或已被删除' : code === 403 ? '你没有权限查看这条帖子' : '加载失败，请稍后重试'
    status.value = null
  } finally {
    if (myVersion === requestVersion) {
      loading.value = false
    }
  }
  if (myVersion === requestVersion && status.value && route.query.reply) {
    await nextTick()
    replyComposer.value?.focus()
  }
}

onMounted(loadPost)
watch(() => route.params.statusId, (id) => { if (id) loadPost() })

// Leave the page when the main post is deleted from its menu.
watch(() => mainStatus.value && deletedStatusIds.has(mainStatus.value.id), (gone) => {
  if (gone) goBack()
})

function onReplied(reply: MastodonStatus) {
  descendants.value.push(reply)
  if (status.value) {
    const target = status.value.reblog ?? status.value
    target.replies_count = (target.replies_count || 0) + 1
  }
}

function focusReply() {
  replyComposer.value?.focus()
}

function goBack() {
  // Check if there's history to go back to, otherwise go to home
  if (window.history.state?.back) {
    router.back()
  } else {
    router.push({ name: 'home' })
  }
}

function openProfile() {
  if (!mainStatus.value) return
  const a = mainStatus.value.account
  router.push({ name: 'profile', params: { username: a.acct || a.username } }).catch(() => {})
}
</script>

<template>
  <div class="detail-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" @click="goBack" aria-label="返回">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title">帖子</h1>
    </div>

    <div v-if="loading" class="page-loading">
      <t-loading />
    </div>

    <div v-else-if="error" class="empty-state" role="alert">
      <div class="empty-state__icon">
        <t-icon name="error-circle" :size="48" />
      </div>
      <h2 class="empty-state__title">无法显示这条帖子</h2>
      <p class="empty-state__text">{{ error }}</p>
    </div>

    <template v-else-if="status && mainStatus">
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
      <article class="detail-main" aria-label="帖子正文">
        <div class="detail-main__header">
          <button type="button" class="detail-main__avatar" :aria-label="`查看 ${mainStatus.account.display_name || mainStatus.account.username} 的个人主页`" @click="openProfile">
            <UserAvatar :account="mainStatus.account" :size="48" />
          </button>
          <div class="detail-main__user">
            <div class="detail-main__name">{{ mainStatus.account.display_name || mainStatus.account.username }}</div>
            <div class="detail-main__username">@{{ mainStatus.account.acct }}</div>
          </div>
        </div>

        <PostContent :status="mainStatus" text-class="detail-main__content rich-text" />

        <div class="detail-main__time">
          <time :datetime="mainStatus.created_at">
            {{ new Date(mainStatus.created_at).toLocaleString() }}
          </time>
          <span v-if="mainStatus.edited_at"> · 已编辑</span>
        </div>

        <div class="detail-main__stats">
          <span><strong>{{ mainStatus.reblogs_count }}</strong> 次转发</span>
          <span><strong>{{ mainStatus.favourites_count }}</strong> 个喜欢</span>
          <span><strong>{{ mainStatus.replies_count }}</strong> 条回复</span>
        </div>

        <PostActions
          :status="mainStatus"
          @reply="focusReply"
          @reblog="toggleReblog(mainStatus)"
          @favourite="toggleFavourite(mainStatus)"
          @bookmark="toggleBookmark(mainStatus)"
        />
      </article>

      <PostComposer ref="replyComposer" :reply-to="mainStatus" @posted="onReplied" />

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

.detail-main__avatar {
  border-radius: var(--radius-full);
  flex-shrink: 0;
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
