<script setup lang="ts">
import { ref, onMounted, watch } from 'vue'
import { useRoute } from 'vue-router'
import type { MastodonAccount, MastodonStatus } from '@/shared/types/mastodon'
import { userApi } from '../../infrastructure/userApi'
import { PROFILE_TABS } from '../../domain/entities'
import PostCard from '@/shared/components/post/PostCard.vue'
import { formatCount } from '@/shared/utils/format'

const route = useRoute()
const account = ref<MastodonAccount | null>(null)
const statuses = ref<MastodonStatus[]>([])
const activeTab = ref('posts')
const loading = ref(true)

async function loadProfile() {
  loading.value = true
  const username = route.params.username as string
  try {
    const acctData = await userApi.lookupAccount(username)
    account.value = acctData
    const statusData = await userApi.getStatuses(acctData.id, { limit: 20, exclude_replies: activeTab.value === 'posts' })
    statuses.value = statusData
  } catch {
    // Profile not found
  } finally {
    loading.value = false
  }
}

onMounted(loadProfile)
watch(() => route.params.username, loadProfile)
</script>

<template>
  <div class="profile-page" v-if="account">
    <!-- Header -->
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <div>
        <h1 class="page-header__title">{{ account.display_name }}</h1>
        <span class="profile-header-bar__count">{{ formatCount(account.statuses_count) }} posts</span>
      </div>
    </div>

    <!-- Banner -->
    <div class="profile-banner">
      <img v-if="account.header" :src="account.header" alt="Banner" class="profile-banner__img" />
      <div v-else class="profile-banner__placeholder" />
    </div>

    <!-- Avatar + Info -->
    <div class="profile-info">
      <div class="profile-info__top">
        <t-avatar :size="'128px'" :image="account.avatar || undefined" shape="circle" class="profile-avatar">
          {{ account.display_name?.charAt(0) || 'U' }}
        </t-avatar>
        <t-button theme="default" shape="round" class="edit-btn">
          Follow
        </t-button>
      </div>

      <h2 class="profile-name">{{ account.display_name }}</h2>
      <div class="profile-username">@{{ account.acct }}</div>

      <div class="profile-bio" v-if="account.note" v-html="account.note" />

      <div class="profile-stats">
        <router-link :to="`/${account.acct}/following`" class="stat-item">
          <strong>{{ formatCount(account.following_count) }}</strong>
          <span>Following</span>
        </router-link>
        <router-link :to="`/${account.acct}/followers`" class="stat-item">
          <strong>{{ formatCount(account.followers_count) }}</strong>
          <span>Followers</span>
        </router-link>
      </div>
    </div>

    <!-- Tabs -->
    <div class="tab-bar" style="margin-top: 16px;">
      <button
        v-for="tab in PROFILE_TABS"
        :key="tab.value"
        class="tab-bar__item"
        :class="{ 'tab-bar__item--active': activeTab === tab.value }"
        @click="activeTab = tab.value"
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
      <PostCard v-for="s in statuses" :key="s.id" :status="s" />
    </template>
  </div>
</template>

<style scoped>
.profile-page {
  min-height: 100vh;
}

.profile-header-bar__count {
  font-size: var(--font-xs);
  color: var(--text-secondary);
}

.profile-banner {
  height: 200px;
  overflow: hidden;
}

.profile-banner__img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.profile-banner__placeholder {
  width: 100%;
  height: 100%;
  background: var(--bg-secondary);
}

.profile-info {
  padding: 12px 16px 0;
}

.profile-info__top {
  display: flex;
  justify-content: space-between;
  align-items: flex-start;
  margin-top: -68px;
}

.profile-avatar {
  border: 4px solid var(--bg-primary);
}

.edit-btn {
  margin-top: 72px;
  font-weight: 700 !important;
  border-color: var(--border-color) !important;
  color: var(--text-primary) !important;
}

.profile-name {
  font-size: var(--font-xl);
  font-weight: 800;
  margin-top: 4px;
}

.profile-username {
  color: var(--text-secondary);
  font-size: var(--font-md);
}

.profile-bio {
  margin-top: 12px;
  font-size: var(--font-md);
  line-height: 1.5;
}

.profile-bio :deep(a) {
  color: var(--accent-blue);
}

.profile-stats {
  display: flex;
  gap: 20px;
  margin-top: 12px;
}

.stat-item {
  font-size: var(--font-sm);
  color: var(--text-secondary);
  text-decoration: none;
}

.stat-item:hover {
  text-decoration: underline;
}

.stat-item strong {
  color: var(--text-primary);
}
</style>
