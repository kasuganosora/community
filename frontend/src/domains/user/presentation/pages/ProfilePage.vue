<script setup lang="ts">
import { ref, computed, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'
import { userApi } from '../../infrastructure/userApi'
import { PROFILE_TABS, type ProfileTabValue } from '../../domain/entities'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import PostCard from '@/shared/components/post/PostCard.vue'
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import { formatCount } from '@/shared/utils/format'
import RichText from '@/shared/components/common/RichText.vue'

const route = useRoute()
const authStore = useAuthStore()
const account = ref<MastodonAccount | null>(null)
const statuses = ref<MastodonStatus[]>([])
const activeTab = ref<ProfileTabValue>('posts')
const loading = ref(true)
const error = ref<string | null>(null)
const following = ref(false)

// Reuse the shared action handlers — optimistic update on the local array.
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

// Prevent duplicate follow/unfollow requests from rapid clicks.
const pendingFollow = ref(false)

// Request version guard — prevents stale responses from a previous
// profile or tab load from overwriting newer data when navigation
// or tab changes happen while a request is still in flight.
let requestVersion = 0

const isOwnProfile = computed(() => account.value?.id === authStore.currentUser?.id)

async function loadProfile() {
  const myVersion = ++requestVersion
  loading.value = true
  error.value = null
  // Clear old data so the error state is reachable if the new
  // profile fails to load, and so stale account info isn't shown.
  account.value = null
  statuses.value = []
  following.value = false
  const username = route.params.username as string
  try {
    const acctData = await userApi.lookupAccount(username)
    if (myVersion !== requestVersion) return
    account.value = acctData
    const params: Record<string, unknown> = { limit: 20 }
    if (activeTab.value === 'posts') params.exclude_replies = true
    else if (activeTab.value === 'media') params.only_media = true
    const statusData = await userApi.getStatuses(acctData.id, params)
    if (myVersion !== requestVersion) return
    statuses.value = statusData
    // Check relationship status
    if (acctData.id) {
      try {
        const rels = await userApi.getRelationships([acctData.id])
        if (myVersion !== requestVersion) return
        following.value = rels[0]?.following ?? false
      } catch {
        // Non-critical — default to not following
      }
    }
  } catch (e) {
    if (myVersion !== requestVersion) return
    console.error('Failed to load profile:', e)
    error.value = e instanceof Error ? e.message : 'Failed to load profile'
  } finally {
    if (myVersion === requestVersion) {
      loading.value = false
    }
  }
}

async function toggleFollow() {
  if (!account.value) return
  if (pendingFollow.value) return
  pendingFollow.value = true

  const targetId = account.value.id
  const wasFollowing = following.value
  // Optimistic update for immediate UI feedback
  following.value = !wasFollowing
  try {
    const rel = wasFollowing
      ? await userApi.unfollow(targetId)
      : await userApi.follow(targetId)
    // Guard: if the user navigated to a different profile while the
    // request was in flight, don't write the result back.
    if (account.value?.id !== targetId) return
    following.value = rel.following
  } catch (e) {
    // Revert optimistic update on failure (only if still on same profile)
    if (account.value?.id === targetId) {
      following.value = wasFollowing
    }
    console.error('Failed to toggle follow:', e)
  } finally {
    pendingFollow.value = false
  }
}

onMounted(loadProfile)
watch(() => route.params.username, loadProfile)

// Only reload statuses on tab change — no need to refetch account/relationship.
// Shares the version guard with loadProfile so a stale response from either
// function is discarded when the other has superseded it.
async function reloadStatuses() {
  if (!account.value) return
  const myVersion = ++requestVersion
  loading.value = true
  try {
    const params: Record<string, unknown> = { limit: 20 }
    if (activeTab.value === 'posts') params.exclude_replies = true
    else if (activeTab.value === 'media') params.only_media = true
    const data = await userApi.getStatuses(account.value.id, params)
    if (myVersion !== requestVersion) return
    statuses.value = data
  } catch (e) {
    if (myVersion !== requestVersion) return
    console.error('Failed to reload statuses:', e)
  } finally {
    if (myVersion === requestVersion) {
      loading.value = false
    }
  }
}
watch(activeTab, reloadStatuses)
</script>

<template>
  <div class="profile-page" v-if="account">
    <!-- Header -->
    <div class="page-header page-header--with-back">
      <button 
        class="page-header__back-btn" 
        @click="$router.back()"
        aria-label="Go back"
      >
        <t-icon name="chevron-left" :size="20" />
      </button>
      <div>
        <h1 class="page-header__title">{{ account.display_name }}</h1>
        <span class="profile-header-bar__count">{{ formatCount(account.statuses_count) }} posts</span>
      </div>
    </div>

    <!-- Banner -->
    <div class="profile-banner">
      <img v-if="account.header" :src="account.header" alt="Profile banner" class="profile-banner__img" loading="lazy" />
      <div v-else class="profile-banner__placeholder" />
    </div>

    <!-- Avatar + Info -->
    <div class="profile-info">
      <div class="profile-info__top">
        <UserAvatar :account="account" :size="128" class="profile-avatar" />
        <t-button v-if="!isOwnProfile" theme="default" shape="round" class="edit-btn"
          :class="{ 'edit-btn--following': following }"
          @click="toggleFollow"
        >
          {{ following ? 'Following' : 'Follow' }}
        </t-button>
      </div>

      <h2 class="profile-name">{{ account.display_name }}</h2>
      <div class="profile-username">@{{ account.acct }}</div>

      <RichText v-if="account.note" class="profile-bio" :html="account.note" />

      <div class="profile-stats">
        <div class="stat-item">
          <strong>{{ formatCount(account.following_count) }}</strong>
          <span>Following</span>
        </div>
        <div class="stat-item">
          <strong>{{ formatCount(account.followers_count) }}</strong>
          <span>Followers</span>
        </div>
      </div>
    </div>

    <!-- Tabs -->
    <div class="tab-bar" style="margin-top: var(--space-4);">
      <button
        v-for="tab in PROFILE_TABS"
        :key="tab.value"
        class="tab-bar__item"
        :class="{ 'tab-bar__item--active': activeTab === tab.value }"
        @click="activeTab = tab.value as ProfileTabValue"
      >
        <span class="tab-bar__text">{{ tab.label }}</span>
        <div v-if="activeTab === tab.value" class="tab-bar__indicator" />
      </button>
    </div>

    <!-- Posts -->
    <div v-if="loading" class="page-loading">
      <t-loading />
    </div>
    <template v-else>
      <PostCard
        v-for="s in statuses"
        :key="s.id"
        :status="s"
        @reblog="toggleReblog"
        @favourite="toggleFavourite"
        @bookmark="toggleBookmark"
      />
    </template>
  </div>

  <div v-else-if="loading" class="page-loading">
    <t-loading />
  </div>

  <div v-else-if="error" class="empty-state" role="alert">
    <div class="empty-state__icon">
      <t-icon name="error-circle" :size="48" />
    </div>
    <h2 class="empty-state__title">Something went wrong</h2>
    <p class="empty-state__text">{{ error }}</p>
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
