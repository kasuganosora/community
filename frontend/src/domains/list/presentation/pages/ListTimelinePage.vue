<script setup lang="ts">
import NewPostsPill from '@/shared/components/common/NewPostsPill.vue'
import { ref, computed, watch, onMounted, nextTick, onBeforeUnmount } from 'vue'
import { useRoute, useRouter } from 'vue-router'
import { MessagePlugin } from '@/shared/ui/feedback'
import type { MastodonAccount, MastodonList, MastodonTag } from '@/shared/types/mastodon'
import { listApi, type RepliesPolicy } from '../../infrastructure/listApi'
import { validateListTitle, REPLIES_POLICY_OPTIONS, MAX_LIST_TITLE_LENGTH, MAX_LIST_TAGS, normalizeTagInput } from '../../domain/list'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { useFeedStream } from '@/shared/composables/useFeedStream'
import { usePaginatedList } from '@/shared/composables/usePaginatedList'
import { useStatusActions, applyStatusUpdate } from '@/domains/post/presentation/composables/useStatusActions'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { userApi } from '@/domains/user/infrastructure/userApi'
import { TIMELINE_PAGE_SIZE } from '@/shared/utils/constants'
import TimelineFeed from '@/domains/timeline/presentation/components/TimelineFeed.vue'
import AccountRow from '@/shared/components/common/AccountRow.vue'

import { t as i18nT } from '@/shared/i18n'
const route = useRoute()
const router = useRouter()
const authStore = useAuthStore()
const listId = computed(() => String(route.params.id ?? ''))
const list = ref<MastodonList | null>(null)
const listError = ref<string | null>(null)

const feed = usePaginatedList(
  (maxId, limit) => listApi.getTimeline(listId.value, { max_id: maxId, limit }),
  TIMELINE_PAGE_SIZE,
  8,
)
const { items: statuses, loading, error, hasMore, newer, hasPrevious } = feed
const streamTarget = computed(() => `stream=list&list=${encodeURIComponent(listId.value)}`)
useFeedStream(streamTarget, feed.revalidate)
async function reloadHead() { await feed.refresh(); if (!error.value) window.scrollTo(0, 0) }
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
    if (route.query.manage === '1') void openManage()
  } catch (e) {
    if (version === loadVersion) listError.value = e instanceof Error ? e.message : i18nT('list.ListTimelinePage.s31')
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
  const version = loadVersion
  if (!list.value) return
  editTitle.value = list.value.title
  editPolicy.value = list.value.replies_policy ?? 'list'
  manageError.value = null
  memberQuery.value = ''
  manageOpen.value = true
  membersLoading.value = true
  try {
    const me = authStore.currentUser
    const [m, following, t] = await Promise.all([
      listApi.getAccounts(listId.value),
      // Lists can only contain accounts you follow.
      me ? userApi.getAllFollowing(me.id) : Promise.resolve([]),
      listApi.getTags(listId.value).catch(() => [] as MastodonTag[]),
    ])
    if (version !== loadVersion) return
    members.value = m
    tags.value = t
    candidates.value = following
  } catch (e) {
    if (version === loadVersion) manageError.value = e instanceof Error ? e.message : i18nT('list.ListTimelinePage.s32')
  } finally {
    if (version === loadVersion) membersLoading.value = false
  }
}

// ---- Hashtags (extension: posts with these tags also show in the list) ----
const tags = ref<MastodonTag[]>([])
const tagQuery = ref('')
const tagSuggestions = ref<MastodonTag[]>([])
const tagKeys = computed(() => new Set(tags.value.map(t => t.name.toLowerCase())))
const visibleSuggestions = computed(() => tagSuggestions.value.filter(t => !tagKeys.value.has(t.name.toLowerCase())))
let suggestTimer: ReturnType<typeof setTimeout> | undefined
let suggestController: AbortController | null = null
watch(tagQuery, (raw) => {
  clearTimeout(suggestTimer)
  suggestController?.abort()
  const q = normalizeTagInput(raw)
  if (!q) { tagSuggestions.value = []; return }
  suggestTimer = setTimeout(async () => {
    const controller = new AbortController()
    suggestController = controller
    try {
      const found = await listApi.searchTags(q, controller.signal)
      if (suggestController === controller) tagSuggestions.value = found
    } catch { /* suggestions are optional */ }
  }, 250)
})

async function addTag(name = tagQuery.value) {
  const tag = normalizeTagInput(name)
  if (!tag) return
  if (tagKeys.value.has(tag.toLowerCase())) { tagQuery.value = ''; return }
  if (tags.value.length >= MAX_LIST_TAGS) { manageError.value = i18nT('list.ListTimelinePage.s33', [MAX_LIST_TAGS]); return }
  const operation = mutationScope()
  busy.value = true
  manageError.value = null
  try {
    const updated = await listApi.addTags(operation.id, [tag])
    if (!operation.current()) return
    tags.value = updated
    tagQuery.value = ''
    tagSuggestions.value = []
  } catch (e) {
    if (operation.current()) manageError.value = e instanceof Error ? e.message : i18nT('list.ListTimelinePage.s34')
  } finally {
    if (operation.current()) busy.value = false
  }
}

async function removeTag(t: MastodonTag) {
  const operation = mutationScope()
  busy.value = true
  manageError.value = null
  try {
    const updated = await listApi.removeTags(operation.id, [t.name])
    if (!operation.current()) return
    tags.value = updated
  } catch (e) {
    if (operation.current()) manageError.value = e instanceof Error ? e.message : i18nT('list.ListTimelinePage.s35')
  } finally {
    if (operation.current()) busy.value = false
  }
}

function mutationScope() {
  const version = loadVersion, id = listId.value, epoch = sessionEpoch.value
  return { id, current: () => version === loadVersion && id === listId.value && epoch === sessionEpoch.value }
}
function resetManage() {
  manageOpen.value = false; confirmDelete.value = false
  members.value = []; candidates.value = []; editTitle.value = ''; editPolicy.value = 'list'
  memberQuery.value = ''; manageError.value = null; busy.value = false; membersLoading.value = false
  tags.value = []; tagQuery.value = ''; tagSuggestions.value = []
}

async function saveSettings() {
  manageError.value = validateListTitle(editTitle.value)
  if (manageError.value || !list.value) return
  const operation = mutationScope()
  busy.value = true
  try {
    const updated = await listApi.updateList(operation.id, editTitle.value.trim(), editPolicy.value)
    if (!operation.current()) return
    list.value = updated
    MessagePlugin.success(i18nT('list.ListTimelinePage.s36'))
  } catch (e) {
    if (operation.current()) manageError.value = e instanceof Error ? e.message : i18nT('list.ListTimelinePage.s37')
  } finally {
    if (operation.current()) busy.value = false
  }
}

async function addMember(a: MastodonAccount) {
  const operation = mutationScope()
  busy.value = true
  manageError.value = null
  try {
    await listApi.addAccounts(operation.id, [a.id])
    if (!operation.current()) return
    members.value = [...members.value, a]
  } catch (e) {
    if (operation.current()) manageError.value = e instanceof Error ? e.message : i18nT('list.ListTimelinePage.s38')
  } finally {
    if (operation.current()) busy.value = false
  }
}

async function removeMember(a: MastodonAccount) {
  const operation = mutationScope()
  busy.value = true
  manageError.value = null
  try {
    await listApi.removeAccounts(operation.id, [a.id])
    if (!operation.current()) return
    members.value = members.value.filter(m => m.id !== a.id)
  } catch (e) {
    if (operation.current()) manageError.value = e instanceof Error ? e.message : i18nT('list.ListTimelinePage.s39')
  } finally {
    if (operation.current()) busy.value = false
  }
}

function closeManage() {
  manageOpen.value = false
  // Membership changes affect the timeline.
  feed.refresh()
}

const confirmDelete = ref(false)
async function deleteList() {
  const operation = mutationScope()
  busy.value = true
  try {
    await listApi.deleteList(operation.id)
    if (!operation.current()) return
    MessagePlugin.success(i18nT('list.ListTimelinePage.s40'))
    manageOpen.value = false
    confirmDelete.value = false
    router.replace('/lists').catch(() => {})
  } catch (e) {
    if (operation.current()) manageError.value = e instanceof Error ? e.message : i18nT('list.ListTimelinePage.s41')
  } finally {
    if (operation.current()) busy.value = false
  }
}

watch(listId, () => { resetManage(); void load() }, { flush: 'sync' })
watch(sessionEpoch, () => {
  ++loadVersion; list.value = null; listError.value = null
  resetManage()
  void nextTick(() => load())
}, { flush: 'sync' })
onBeforeUnmount(() => { ++loadVersion })
onMounted(load)
</script>

<template>
  <div class="list-timeline-page">
    <div class="page-header page-header--with-back">
      <button class="page-header__back-btn" type="button" :aria-label="$t('list.ListTimelinePage.s1')" @click="$router.back()">
        <t-icon name="chevron-left" :size="20" />
      </button>
      <h1 class="page-header__title list-timeline-page__title">{{ list?.title ?? $t('list.ListTimelinePage.s2') }}</h1>
      <button v-if="list" type="button" class="btn btn--secondary btn--sm" @click="openManage">{{ $t('list.ListTimelinePage.s3') }}</button>
    </div>

    <div v-if="listError" class="empty-state" role="alert">
      <div class="empty-state__icon"><t-icon name="error-circle" :size="48" /></div>
      <h2 class="empty-state__title">{{ $t('list.ListTimelinePage.s4') }}</h2>
      <p class="empty-state__text">{{ listError }}</p>
      <router-link to="/lists" class="btn btn--secondary btn--sm">{{ $t('list.ListTimelinePage.s5') }}</router-link>
    </div>

    <NewPostsPill :show="newer.length > 0" :reveal="reloadHead" />
    <TimelineFeed
      v-if="list && !listError"
      :statuses="statuses"
      :scope-key="streamTarget"
      :has-previous="hasPrevious"
      @reload-head="reloadHead"
      @load-previous="feed.loadPrevious"
      @pinned-ids="feed.setPinnedIds"
      :loading="loading"
      :has-more="hasMore"
      :error="error"
      :empty-title="$t('list.ListTimelinePage.s6')"
      :empty-text="$t('list.ListTimelinePage.s7')"
      @load-more="feed.loadMore"
      @retry="statuses.length && hasMore ? feed.loadMore() : feed.refresh()"
      @reblog="toggleReblog"
      @favourite="toggleFavourite"
      @bookmark="toggleBookmark"
    />
    <div v-else-if="!listError" class="page-loading"><t-loading /></div>

    <t-dialog
      v-model:visible="manageOpen"
      :header="$t('list.ListTimelinePage.s8')"
      :footer="false"
      width="560px"
      destroy-on-close
      @close="closeManage"
    >
      <form class="manage-form" @submit.prevent="saveSettings">
        <label class="manage-field">
          <span class="manage-field__label">{{ $t('list.ListTimelinePage.s9') }}</span>
          <input v-model="editTitle" class="manage-input" type="text" :maxlength="MAX_LIST_TITLE_LENGTH" />
        </label>
        <label class="manage-field">
          <span class="manage-field__label">{{ $t('list.ListTimelinePage.s10') }}</span>
          <select v-model="editPolicy" class="manage-input">
            <option v-for="o in REPLIES_POLICY_OPTIONS" :key="o.value" :value="o.value">{{ o.label }}</option>
          </select>
        </label>
        <div class="manage-actions">
          <button type="submit" class="btn btn--primary btn--sm" :disabled="busy">{{ $t('list.ListTimelinePage.s11') }}</button>
        </div>
      </form>

      <p v-if="manageError" class="manage-error" role="alert">{{ manageError }}</p>

      <h3 class="manage-subtitle">{{ $t('list.ListTimelinePage.s12', [tags.length, MAX_LIST_TAGS]) }}</h3>
      <p class="manage-hint manage-hint--tight">{{ $t('list.ListTimelinePage.s13') }}</p>
      <ul v-if="tags.length" class="tag-chips" :aria-label="$t('list.ListTimelinePage.s14')">
        <li v-for="t in tags" :key="t.name" class="tag-chip">
          <span>#{{ t.name }}</span>
          <button type="button" class="tag-chip__remove" :aria-label="$t('list.ListTimelinePage.s15', [t.name])" :disabled="busy" @click="removeTag(t)">
            <t-icon name="close" :size="14" />
          </button>
        </li>
      </ul>
      <form v-if="tags.length < MAX_LIST_TAGS" class="tag-add" @submit.prevent="addTag()">
        <label for="list-tag-input" class="sr-only">{{ $t('list.ListTimelinePage.s16') }}</label>
        <input id="list-tag-input" v-model="tagQuery" class="manage-input" type="text" inputmode="text" enterkeyhint="done"
          autocomplete="off" autocapitalize="off" :placeholder="$t('list.ListTimelinePage.s17')" maxlength="100"
/>
        <button type="submit" class="btn btn--secondary btn--sm manage-row-btn" :disabled="busy || !tagQuery.trim()">{{ $t('list.ListTimelinePage.s16') }}</button>
      </form>
      <ul v-if="visibleSuggestions.length" class="tag-suggestions" role="listbox" :aria-label="$t('list.ListTimelinePage.s18')">
        <li v-for="s in visibleSuggestions" :key="s.name">
          <button type="button" role="option" :aria-selected="false" :disabled="busy" @click="addTag(s.name)">#{{ s.name }}</button>
        </li>
      </ul>

      <h3 class="manage-subtitle">{{ $t('list.ListTimelinePage.s19', [members.length]) }}</h3>
      <div v-if="membersLoading" class="page-loading"><t-loading /></div>
      <template v-else>
        <p v-if="members.length === 0" class="manage-hint">{{ $t('list.ListTimelinePage.s20') }}</p>
        <AccountRow v-for="m in members" :key="m.id" :account="m">
          <template #action>
            <button type="button" class="btn btn--ghost btn--sm manage-row-btn" :disabled="busy" @click.stop="removeMember(m)">{{ $t('list.ListTimelinePage.s21') }}</button>
          </template>
        </AccountRow>

        <h3 class="manage-subtitle">{{ $t('list.ListTimelinePage.s22') }}</h3>
        <label for="member-filter" class="sr-only">{{ $t('list.ListTimelinePage.s23') }}</label>
        <input id="member-filter" v-model="memberQuery" class="manage-input" type="search" :placeholder="$t('list.ListTimelinePage.s23')" />
        <p v-if="filteredCandidates.length === 0" class="manage-hint">
          {{ candidates.length === 0 ? $t('list.ListTimelinePage.s24') : $t('list.ListTimelinePage.s25') }}
        </p>
        <AccountRow v-for="a in filteredCandidates" :key="a.id" :account="a">
          <template #action>
            <button type="button" class="btn btn--secondary btn--sm manage-row-btn" :disabled="busy" @click.stop="addMember(a)">{{ $t('list.ListTimelinePage.s26') }}</button>
          </template>
        </AccountRow>
      </template>

      <div class="manage-danger">
        <template v-if="!confirmDelete">
          <button type="button" class="btn btn--danger btn--sm" @click="confirmDelete = true">{{ $t('list.ListTimelinePage.s27') }}</button>
        </template>
        <template v-else>
          <span>{{ $t('list.ListTimelinePage.s28', [list?.title]) }}</span>
          <button type="button" class="btn btn--ghost btn--sm" @click="confirmDelete = false">{{ $t('list.ListTimelinePage.s29') }}</button>
          <button type="button" class="btn btn--danger btn--sm" :disabled="busy" @click="deleteList">{{ $t('list.ListTimelinePage.s30') }}</button>
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

/* The native popup ignores the dark theme and paints light text on white. */
select.manage-input {
  color-scheme: light;
  background: #fff;
  color: #1c1c1e;
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

/* Row actions sit in a flex row that stretches children to the row height
   (a long bio made "添加" a tall pill); keep them compact and centered. */
.manage-row-btn {
  flex: none;
  align-self: center;
  height: auto;
  white-space: nowrap;
}

.manage-hint--tight {
  padding: 0 0 var(--space-2);
}

.tag-chips {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin: 0 0 var(--space-2);
  padding: 0;
  list-style: none;
}

.tag-chip {
  display: inline-flex;
  align-items: center;
  gap: 2px;
  padding: 2px 4px 2px 10px;
  border-radius: 999px;
  background: var(--color-fill, rgb(120 120 128 / 12%));
  font-size: var(--font-sm);
}

.tag-chip__remove {
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 24px;
  height: 24px;
  border: 0;
  border-radius: 50%;
  background: transparent;
  color: var(--color-label-secondary, var(--text-secondary));
  cursor: pointer;
}

.tag-add {
  display: flex;
  align-items: center;
  gap: var(--space-2);
}

.tag-suggestions {
  display: flex;
  flex-wrap: wrap;
  gap: var(--space-2);
  margin: var(--space-2) 0 0;
  padding: 0;
  list-style: none;
}

.tag-suggestions button {
  padding: 4px 10px;
  border: 1px solid var(--color-separator, var(--border-color));
  border-radius: 999px;
  background: transparent;
  color: var(--color-accent, inherit);
  font: inherit;
  font-size: var(--font-sm);
  cursor: pointer;
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
