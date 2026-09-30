<script setup lang="ts">
import { ref, watch, onMounted, computed } from 'vue'
import { useRoute } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { userApi } from '../../infrastructure/userApi'
import { useRelationships } from '../composables/useRelationships'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import AccountRow from '@/shared/components/common/AccountRow.vue'
import TabBar from '@/shared/components/common/TabBar.vue'

const props = defineProps<{ kind: 'followers' | 'following' }>()

// The backend paginates these by internal follow-row id and sends no Link
// header, so the last account id can't be used as a cursor: fetch one large page.
const PAGE_LIMIT = 80

const route = useRoute()
const authStore = useAuthStore()
const owner = ref<MastodonAccount | null>(null)
const accounts = ref<MastodonAccount[]>([])
const loading = ref(true)
const error = ref<string | null>(null)
const relationships = useRelationships()
let version = 0

const title = computed(() => props.kind === 'followers' ? '关注者' : '正在关注')
const followTabs = computed(() => {
  const name = String(route.params.username ?? '')
  return [
    { value: 'followers' as const, label: '关注者', to: `/@${name}/followers` },
    { value: 'following' as const, label: '正在关注', to: `/@${name}/following` },
  ]
})

async function load() {
  const my = ++version
  loading.value = true
  error.value = null
  accounts.value = []
  try {
    const acct = await userApi.lookupAccount(String(route.params.username ?? ''))
    if (my !== version) return
    owner.value = acct
    const list = props.kind === 'followers'
      ? await userApi.getFollowers(acct.id, { limit: PAGE_LIMIT })
      : await userApi.getFollowing(acct.id, { limit: PAGE_LIMIT })
    if (my !== version) return
    accounts.value = list
    relationships.load(list.filter(a => a.id !== authStore.currentUser?.id).map(a => a.id))
  } catch (e) {
    if (my !== version) return
    error.value = e instanceof Error ? e.message : '加载失败'
  } finally {
    if (my === version) loading.value = false
  }
}

async function toggleFollow(account: MastodonAccount) {
  try {
    await relationships.toggleFollow(account.id)
  } catch (e) {
    MessagePlugin.error(e instanceof Error ? e.message : '操作失败')
  }
}

watch(() => [route.params.username, props.kind], load)
onMounted(load)
</script>

<template>
  <div class="follow-list-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" type="button" aria-label="返回" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <div>
        <h1 class="page-header__title">{{ title }}</h1>
        <span v-if="owner" class="follow-list-page__owner">{{ owner.display_name || owner.username }} · @{{ owner.acct }}</span>
      </div>
    </div>

    <TabBar :model-value="kind" :options="followTabs" label="关注关系" />

    <div v-if="loading" class="page-loading"><t-loading /></div>

    <div v-else-if="error" class="empty-state" role="alert">
      <div class="empty-state__icon"><t-icon name="error-circle" :size="48" /></div>
      <h2 class="empty-state__title">加载失败</h2>
      <p class="empty-state__text">{{ error }}</p>
      <button type="button" class="btn btn--secondary btn--sm" @click="load">重试</button>
    </div>

    <div v-else-if="accounts.length === 0" class="empty-state">
      <div class="empty-state__icon"><t-icon name="user" :size="48" /></div>
      <h2 class="empty-state__title">{{ kind === 'followers' ? '还没有关注者' : '还没有关注任何人' }}</h2>
      <p v-if="owner?.hide_collections" class="empty-state__text">该用户隐藏了关注关系。</p>
    </div>

    <template v-else>
      <AccountRow
        v-for="a in accounts"
        :key="a.id"
        :account="a"
        :follow-state="a.id === authStore.currentUser?.id ? undefined : relationships.state(a.id)"
        :followed-by="relationships.rels.get(a.id)?.followed_by"
        :busy="relationships.pending.has(a.id)"
        @toggle-follow="toggleFollow"
      />
    </template>
  </div>
</template>

<style scoped>
.follow-list-page {
  min-height: 100vh;
}

.follow-list-page__owner {
  font-size: var(--font-xs);
  color: var(--color-label-secondary, var(--text-secondary));
}

.tab-bar__item {
  text-decoration: none;
  color: inherit;
}
</style>
