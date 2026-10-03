<script setup lang="ts">
import EmojiText from '@/shared/components/common/EmojiText.vue'
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'
import { userApi } from '../../infrastructure/userApi'
import { PROFILE_TABS, type ProfileTabValue } from '../../domain/entities'
import { followButtonLabel } from '../../domain/relationship'
import { useRelationships } from '../composables/useRelationships'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import type { CursorPage } from '@/shared/utils/pagination'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import TimelineFeed from '@/domains/timeline/presentation/components/TimelineFeed.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { formatCount } from '@/shared/utils/format'
import RichText from '@/shared/components/common/RichText.vue'
import TabBar from '@/shared/components/common/TabBar.vue'

const route = useRoute()
const authStore = useAuthStore()
const account = ref<MastodonAccount | null>(null)
const activeTab = ref<ProfileTabValue>('posts')
const loading = ref(true)
const error = ref<string | null>(null)
const relationships = useRelationships()

// Request version guard — a slow lookup for a previous profile must not
// overwrite the profile we navigated to.
let requestVersion = 0

const isOwnProfile = computed(() => !!account.value && account.value.id === authStore.currentUser?.id)
const visibleTabs = computed(() => PROFILE_TABS.filter(t => !t.ownOnly || isOwnProfile.value))
const relationship = computed(() => account.value ? relationships.rels.get(account.value.id) : undefined)
const followState = computed(() => account.value ? relationships.state(account.value.id) : 'unknown')
const profileBase = computed(() => `/@${route.params.username as string}`)

function fetchStatuses(maxId: string | undefined, limit: number): Promise<MastodonStatus[] | CursorPage<MastodonStatus>> {
  const acct = account.value
  if (!acct) return Promise.resolve([])
  // Likes use the relationship cursor supplied by the server's Link header.
  if (activeTab.value === 'likes') return userApi.getFavourites({ max_id: maxId, limit })
  return userApi.getStatuses(acct.id, {
    max_id: maxId,
    limit,
    ...(activeTab.value === 'posts' ? { exclude_replies: true } : {}),
    ...(activeTab.value === 'media' ? { only_media: true } : {}),
  })
}

const feed = usePaginatedList(fetchStatuses, TIMELINE_PAGE_SIZE)
const { items: statuses } = feed

const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

async function loadProfile() {
  const myVersion = ++requestVersion
  loading.value = true
  error.value = null
  account.value = null
  statuses.value = []
  activeTab.value = 'posts'
  const username = route.params.username as string
  try {
    const acctData = await userApi.lookupAccount(username)
    if (myVersion !== requestVersion) return
    account.value = acctData
    feed.refresh()
    if (acctData.id !== authStore.currentUser?.id) relationships.load([acctData.id])
  } catch (e) {
    if (myVersion !== requestVersion) return
    console.error('Failed to load profile:', e)
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    if (myVersion === requestVersion) loading.value = false
  }
}

async function toggleFollow() {
  if (!account.value) return
  const targetId = account.value.id
  const wasFollowing = relationships.state(targetId) === 'following'
  try {
    await relationships.toggleFollow(targetId)
    // Keep the follower count in step with the button.
    if (account.value?.id === targetId) {
      const following = relationships.state(targetId) === 'following'
      account.value.followers_count = Math.max(0, account.value.followers_count + Number(following) - Number(wasFollowing))
    }
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : '操作失败')
  }
}

onMounted(loadProfile)
watch(() => route.params.username, loadProfile)
watch(activeTab, () => { if (account.value) feed.refresh() })
</script>

<template>
  <div class="profile-page" v-if="account">
    <!-- Header -->
    <div class="page-header page-header--with-back">
      <button
        class="page-header__back-btn"
        type="button"
        @click="$router.back()"
        aria-label="返回"
      >
        <t-icon name="chevron-left" :size="20" />
      </button>
      <div>
        <h1 class="page-header__title"><EmojiText :text="account.display_name || account.username" :emojis="account.emojis" /></h1>
        <span class="profile-header-bar__count">{{ formatCount(account.statuses_count) }} 条帖子</span>
      </div>
    </div>

    <!-- Banner -->
    <div class="profile-banner">
      <img v-if="account.header" :src="account.header" alt="" class="profile-banner__img" loading="lazy" />
      <div v-else class="profile-banner__placeholder" />
    </div>

    <!-- Avatar + Info -->
    <div class="profile-info">
      <div class="profile-info__top">
        <UserAvatar :account="account" :size="128" class="profile-avatar" />
        <t-button v-if="isOwnProfile" theme="default" shape="round" class="edit-btn" @click="$router.push('/settings')">
          编辑个人资料
        </t-button>
        <t-button
          v-else-if="followState !== 'unknown'"
          theme="default"
          shape="round"
          class="edit-btn"
          :class="{ 'edit-btn--following': followState !== 'none' }"
          :loading="relationships.pending.has(account.id)"
          @click="toggleFollow"
        >
          {{ followButtonLabel(followState, relationship?.followed_by) }}
        </t-button>
      </div>

      <h2 class="profile-name"><EmojiText :text="account.display_name || account.username" :emojis="account.emojis" /></h2>
      <div class="profile-username">
        @{{ account.acct }}
        <span v-if="relationship?.followed_by" class="profile-follows-you">关注了你</span>
      </div>

      <RichText v-if="account.note" class="profile-bio" :html="account.note" :emojis="account.emojis" />

      <div class="profile-stats">
        <router-link :to="`${profileBase}/following`" class="stat-item">
          <strong>{{ formatCount(account.following_count) }}</strong>
          <span> 正在关注</span>
        </router-link>
        <router-link :to="`${profileBase}/followers`" class="stat-item">
          <strong>{{ formatCount(account.followers_count) }}</strong>
          <span> 关注者</span>
        </router-link>
      </div>
    </div>

    <!-- Tabs -->
    <TabBar
      v-model="activeTab"
      :options="visibleTabs"
      label="个人主页"
      style="margin-top: var(--space-4)"
    />

    <!-- Posts -->
    <TimelineFeed
      :statuses="statuses"
      :loading="feed.loading.value"
      :has-more="feed.hasMore.value"
      :error="feed.error.value"
      empty-title="暂无内容"
      :empty-text="activeTab === 'likes' ? '你喜欢的帖子会显示在这里。' : '这里还没有帖子。'"
      @load-more="feed.loadMore"
      @retry="feed.refresh"
      @reblog="toggleReblog"
      @favourite="toggleFavourite"
      @bookmark="toggleBookmark"
    />
  </div>

  <div v-else-if="loading" class="page-loading">
    <t-loading />
  </div>

  <div v-else-if="error" class="empty-state" role="alert">
    <div class="empty-state__icon">
      <t-icon name="error-circle" :size="48" />
    </div>
    <h2 class="empty-state__title">无法加载此用户</h2>
    <p class="empty-state__text">{{ error }}</p>
    <button type="button" class="btn btn--secondary btn--sm" @click="loadProfile">重试</button>
  </div>
</template>

<style scoped>
.profile-page {
  min-height: 100dvh;
}

.page-header--with-back > div {
  min-width: 0;
  display: flex;
  flex-direction: column;
}

.page-header--with-back .page-header__title {
  overflow: hidden;
  text-overflow: ellipsis;
  white-space: nowrap;
}

.profile-header-bar__count {
  font-size: var(--text-caption);
  letter-spacing: var(--tracking-caption);
  color: var(--color-label-secondary);
  font-variant-numeric: tabular-nums;
}

.profile-banner {
  aspect-ratio: 3 / 1;
  max-height: 12.5rem;
  overflow: hidden;
  background: var(--color-fill);
}

.profile-banner__img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.profile-banner__placeholder {
  width: 100%;
  height: 100%;
  background: linear-gradient(135deg, var(--color-fill), var(--color-fill-strong));
}

.profile-info {
  padding: 0 var(--gutter);
}

.profile-info__top {
  display: flex;
  justify-content: space-between;
  align-items: flex-end;
  gap: var(--space-3);
  margin-top: calc(-1 * var(--profile-avatar) / 2);
  --profile-avatar: 6.5rem;
}

/* Avatar sits on the banner edge with a ring in the page colour. */
.profile-avatar {
  width: var(--profile-avatar) !important;
  height: var(--profile-avatar) !important;
  font-size: calc(var(--profile-avatar) * 0.4) !important;
  box-shadow: 0 0 0 4px var(--color-bg), var(--shadow-1);
}

/* Follow is the one prominent action: a label-coloured capsule.
   Once following it recedes into a quiet outlined capsule. */
.edit-btn {
  margin-bottom: var(--space-1);
  min-width: 6rem;
  font-weight: var(--weight-semibold) !important;
  --td-bg-color-component: var(--color-label);
  background: var(--color-label) !important;
  border-color: var(--color-label) !important;
  color: var(--color-bg) !important;
}

.edit-btn--following {
  background: transparent !important;
  border-color: var(--color-separator-opaque) !important;
  color: var(--color-label) !important;
}

.profile-name {
  margin-top: var(--space-3);
  font-size: var(--text-title-2);
  line-height: var(--leading-title-2);
  letter-spacing: var(--tracking-title-2);
  font-weight: var(--weight-bold);
  color: var(--color-label);
  overflow-wrap: anywhere;
}

.profile-username {
  color: var(--color-label-secondary);
  font-size: var(--text-subhead);
}

.profile-bio {
  margin-top: var(--space-3);
  font-size: var(--text-body);
  line-height: var(--leading-body);
  color: var(--color-label);
}

.profile-bio:lang(zh) {
  line-height: var(--leading-cjk);
}

.profile-stats {
  display: flex;
  gap: var(--space-5);
  margin-top: var(--space-3);
}

.stat-item {
  display: inline-flex;
  gap: var(--space-1);
  font-size: var(--text-subhead);
  color: var(--color-label-secondary);
}

.profile-follows-you {
  margin-left: var(--space-2);
  padding: 0 var(--space-2);
  border-radius: var(--radius-sm, 4px);
  background: var(--color-fill, var(--bg-secondary));
  font-size: var(--font-xs);
}

.stat-item strong {
  color: var(--color-label);
  font-weight: var(--weight-semibold);
  font-variant-numeric: tabular-nums;
}

@media (min-width: 43.75rem) {
  .profile-info__top {
    --profile-avatar: 8rem;
  }
}
</style>
