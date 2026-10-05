<script setup lang="ts">
import { ref, computed, watch, onMounted } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from 'tdesign-vue-next'
import type { MastodonAccount, MastodonList } from '@/shared/types/mastodon'
import { listApi, type RepliesPolicy } from '../../infrastructure/listApi'
import { validateListTitle, REPLIES_POLICY_OPTIONS, MAX_LIST_TITLE_LENGTH } from '../../domain/list'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import TimelineFeed from '@/domains/timeline/presentation/components/TimelineFeed.vue'
import AccountRow from '@/shared/components/common/AccountRow.vue'

const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const listId = computed(() => String(route.params.id ?? ''))
const list = ref<MastodonList | null>(null)
const listError = ref<string | null>(null)

const feed = usePaginatedList(
  (maxId, limit) => listApi.getTimeline(listId.value, { max_id: maxId, limit }),
  TIMELINE_PAGE_SIZE,
)
const { items: statuses, loading, error, hasMore } = feed
const { toggleReblog, toggleFavourite, toggleBookmark } = useStatusActions(
  (id, updates) => applyStatusUpdate(statuses.value, id, updates),
)

let loadVersion = 0
async function load() {
  const version = ++loadVersion
  const id = listId.value
  feed.reset()
  listError.value = null
  list.value = null
  try {
    const result = await listApi.getList(id)
    if (version !== loadVersion) return
    list.value = result
    feed.refresh()
  } catch (e) {
    if (version === loadVersion) listError.value = e instanceof Error ? e.message : '列表不存在'
  }
}

// ---- Manage dialog ----
const manageOpen = ref(false)
const editTitle = ref('')
const editPolicy = ref<RepliesPolicy>('list')
const manageError = ref<string | null>(null)
const members = ref<MastodonAccount[]>([])
const membersLoading = ref(false)
const candidates = ref<MastodonAccount[]>([])
const memberQuery = ref('')
const busy = ref(false)

const memberIds = computed(() => new Set(members.value.map(m => m.id)))
const filteredCandidates = computed(() => {
  const q = memberQuery.value.trim().toLowerCase()
  return candidates.value
    .filter(a => !memberIds.value.has(a.id))
    .filter(a => !q || a.acct.toLowerCase().includes(q) || (a.display_name || '').toLowerCase().includes(q))
})

async function openManage() {
  if (!list.value) return
  editTitle.value = list.value.title
  editPolicy.value = list.value.replies_policy ?? 'list'
  manageError.value = null
  memberQuery.value = ''
  manageOpen.value = true
  membersLoading.value = true
  try {
    const me = authStore.currentUser
    const [m, following] = await Promise.all([
      listApi.getAccounts(listId.value),
      // Lists can only contain accounts you follow.
      me ? userApi.getAllFollowing(me.id) : Promise.resolve([]),
    ])
    members.value = m
    candidates.value = following
  } catch (e) {
    manageError.value = e instanceof Error ? e.message : '加载成员失败'
  } finally {
    membersLoading.value = false
  }
}

async function saveSettings() {
  manageError.value = validateListTitle(editTitle.value)
  if (manageError.value || !list.value) return
  busy.value = true
  try {
    list.value = await listApi.updateList(listId.value, editTitle.value.trim(), editPolicy.value)
    MessagePlugin.success('已保存')
  } catch (e) {
    manageError.value = e instanceof Error ? e.message : '保存失败'
  } finally {
    busy.value = false
  }
}

async function addMember(a: MastodonAccount) {
  busy.value = true
  manageError.value = null
  try {
    await listApi.addAccounts(listId.value, [a.id])
    members.value = [...members.value, a]
  } catch (e) {
    manageError.value = e instanceof Error ? e.message : '添加失败'
  } finally {
    busy.value = false
  }
}

async function removeMember(a: MastodonAccount) {
  busy.value = true
  manageError.value = null
  try {
    await listApi.removeAccounts(listId.value, [a.id])
    members.value = members.value.filter(m => m.id !== a.id)
  } catch (e) {
    manageError.value = e instanceof Error ? e.message : '移除失败'
  } finally {
    busy.value = false
  }
}

function closeManage() {
  manageOpen.value = false
  // Membership changes affect the timeline.
  feed.refresh()
}

const confirmDelete = ref(false)
async function deleteList() {
  busy.value = true
  try {
    await listApi.deleteList(listId.value)
    MessagePlugin.success('列表已删除')
    manageOpen.value = false
    confirmDelete.value = false
    router.replace('/lists').catch(() => {})
  } catch (e) {
    manageError.value = e instanceof Error ? e.message : '删除失败'
  } finally {
    busy.value = false
  }
}

watch(listId, load)
onMounted(load)
</script>

<template>
  <div class="list-timeline-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" type="button" aria-label="返回" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title list-timeline-page__title">{{ list?.title ?? '列表' }}</h1>
      <button v-if="list" type="button" class="btn btn--secondary btn--sm" @click="openManage">管理</button>
    </div>

    <div v-if="listError" class="empty-state" role="alert">
      <div class="empty-state__icon"><t-icon name="error-circle" :size="48" /></div>
      <h2 class="empty-state__title">无法打开列表</h2>
      <p class="empty-state__text">{{ listError }}</p>
      <router-link to="/lists" class="btn btn--secondary btn--sm">返回列表</router-link>
    </div>

    <TimelineFeed
      v-else-if="list"
      :statuses="statuses"
      :loading="loading"
      :has-more="hasMore"
      :error="error"
      empty-title="列表时间线为空"
      empty-text="点击「管理」添加成员，他们的新帖子会出现在这里。"
      @load-more="feed.loadMore"
      @retry="statuses.length && hasMore ? feed.loadMore() : feed.refresh()"
      @reblog="toggleReblog"
      @favourite="toggleFavourite"
      @bookmark="toggleBookmark"
    />
    <div v-else class="page-loading"><t-loading /></div>

    <t-dialog
      v-model:visible="manageOpen"
      header="管理列表"
      :footer="false"
      width="560px"
      destroy-on-close
      @close="closeManage"
    >
      <form class="manage-form" @submit.prevent="saveSettings">
        <label class="manage-field">
          <span class="manage-field__label">名称</span>
          <input v-model="editTitle" class="manage-input" type="text" :maxlength="MAX_LIST_TITLE_LENGTH" />
        </label>
        <label class="manage-field">
          <span class="manage-field__label">回复显示</span>
          <select v-model="editPolicy" class="manage-input">
            <option v-for="o in REPLIES_POLICY_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
          </select>
        </label>
        <div class="manage-actions">
          <button type="submit" class="btn btn--primary btn--sm" :disabled="busy">保存</button>
        </div>
      </form>

      <p v-if="manageError" class="manage-error" role="alert">{{ manageError }}</p>

      <h3 class="manage-subtitle">成员（{{ members.length }}）</h3>
      <div v-if="membersLoading" class="page-loading"><t-loading /></div>
      <template v-else>
        <p v-if="members.length === 0" class="manage-hint">还没有成员。</p>
        <AccountRow v-for="m in members" :key="m.id" :account="m">
          <template #action>
            <button type="button" class="btn btn--ghost btn--sm" :disabled="busy" @click.stop="removeMember(m)">移除</button>
          </template>
        </AccountRow>

        <h3 class="manage-subtitle">从关注的人中添加</h3>
        <label for="member-filter" class="sr-only">筛选关注的人</label>
        <input id="member-filter" v-model="memberQuery" class="manage-input" type="search" placeholder="筛选关注的人" />
        <p v-if="filteredCandidates.length === 0" class="manage-hint">
          {{ candidates.length === 0 ? '你还没有关注任何人（列表只能添加已关注的账号）。' : '没有更多可添加的人。' }}
        </p>
        <AccountRow v-for="a in filteredCandidates" :key="a.id" :account="a">
          <template #action>
            <button type="button" class="btn btn--secondary btn--sm" :disabled="busy" @click.stop="addMember(a)">添加</button>
          </template>
        </AccountRow>
      </template>

      <div class="manage-danger">
        <template v-if="!confirmDelete">
          <button type="button" class="btn btn--danger btn--sm" @click="confirmDelete = true">删除列表</button>
        </template>
        <template v-else>
          <span>确定删除「{{ list?.title }}」？此操作无法撤销。</span>
          <button type="button" class="btn btn--ghost btn--sm" @click="confirmDelete = false">取消</button>
          <button type="button" class="btn btn--danger btn--sm" :disabled="busy" @click="deleteList">删除</button>
        </template>
      </div>
    </t-dialog>
  </div>
</template>

<style scoped>
.list-timeline-page {
  min-height: 100vh;
  min-height: 100dvh;
}

.list-timeline-page__title {
  flex: 1;
  min-width: 0;
}

.manage-form {
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
}

.manage-field {
  display: flex;
  flex-direction: column;
  gap: var(--space-1);
}

.manage-field__label,
.manage-subtitle {
  font-size: var(--font-sm);
  font-weight: 600;
}

.manage-subtitle {
  margin: var(--space-4) 0 var(--space-2);
}

.manage-input {
  width: 100%;
  padding: var(--space-2) var(--space-3);
  border: 1px solid var(--color-separator, var(--border-color));
  border-radius: var(--radius-sm, 8px);
  background: transparent;
  color: inherit;
  font: inherit;
}

.manage-actions {
  display: flex;
  justify-content: flex-end;
}

.manage-error {
  margin-top: var(--space-2);
  color: var(--color-danger, var(--accent-red, #e0245e));
  font-size: var(--font-sm);
}

.manage-hint {
  padding: var(--space-2) 0;
  font-size: var(--font-sm);
  color: var(--color-label-secondary, var(--text-secondary));
}

.manage-danger {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin-top: var(--space-5);
  padding-top: var(--space-3);
  border-top: var(--hairline, 1px) solid var(--color-separator, var(--border-color));
  font-size: var(--font-sm);
}
</style>
