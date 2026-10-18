<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { exploreGroupsApi, type ExploreGroup } from '../../infrastructure/exploreGroupsApi'
import { groupApi } from '../../infrastructure/groupApi'
import { groupPath } from '../../domain/group'
import GroupAvatar from './GroupAvatar.vue'
import { t } from '@/shared/i18n'

const router = useRouter()
const auth = useAuthStore()
const groups = ref<ExploreGroup[]>([])
const next = ref<string | null>(null)
const loading = ref(false)
const error = ref('')
const done = ref(false)
const joining = ref<Record<string, 'busy' | 'requested' | 'joining' | string>>({})
const sentinel = ref<HTMLElement | null>(null)
let controller: AbortController | undefined
let observer: IntersectionObserver | undefined

// Stable backend error codes → localized messages.
const ERRORS: Record<string, string> = {
  EXPLORE_GROUPS_INVALID_CURSOR: 'group.ExploreGroups.errCursor',
  EXPLORE_GROUPS_INVALID_LIMIT: 'group.ExploreGroups.errLoad',
  EXPLORE_GROUPS_UNAVAILABLE: 'group.ExploreGroups.errLoad',
  SIGNIN_REQUIRED: 'group.ExploreGroups.errSignin',
  CONTENT_RESTRICTED_BY_SERVER: 'group.ExploreGroups.errSignin',
  join_requirements: 'group.ExploreGroups.errJoinRules',
  forbidden: 'group.ExploreGroups.errJoinForbidden',
}
function message(e: unknown, fallback: string): string {
  const code = (e as { response?: { data?: { code?: string } } })?.response?.data?.code
  return t(code && ERRORS[code] ? ERRORS[code]! : fallback)
}

async function load(reset = false) {
  if (loading.value || (!reset && done.value)) return
  if (reset) { controller?.abort(); groups.value = []; next.value = null; done.value = false }
  controller = new AbortController()
  const signal = controller.signal
  loading.value = true; error.value = ''
  try {
    const page = await exploreGroupsApi.list(reset ? null : next.value, signal)
    if (signal.aborted) return
    const seen = new Set(groups.value.map(g => g.id))
    groups.value.push(...page.groups.filter(g => !seen.has(g.id)))
    next.value = page.next; done.value = !page.next
  } catch (e) {
    if (!signal.aborted) error.value = message(e, 'group.ExploreGroups.errLoad')
  } finally { if (!signal.aborted) loading.value = false }
}

async function join(g: ExploreGroup) {
  if (!auth.currentUser) { void router.push({ path: '/login', query: { redirect: router.currentRoute.value.fullPath } }); return }
  if (g.membership === 'join') { void router.push(groupPath(g.id)); return }
  joining.value[g.id] = 'busy'
  try {
    const res = await groupApi.join({ target: g.target, via: g.via })
    if (res.status === 'knock') { joining.value[g.id] = 'requested'; return }
    if (res.status === 'joining') { joining.value[g.id] = 'joining'; return }
    g.membership = 'join'; delete joining.value[g.id]
    void router.push(groupPath(res.group?.id || g.id))
  } catch (e) {
    joining.value[g.id] = message(e, 'group.ExploreGroups.errJoin')
  }
}

function label(g: ExploreGroup): string {
  const s = joining.value[g.id]
  if (s === 'busy' || s === 'joining') return t('group.ExploreGroups.joining')
  if (s === 'requested' || g.membership === 'knock') return t('group.ExploreGroups.requested')
  if (g.membership === 'join') return t('group.ExploreGroups.open')
  if (g.membership === 'invite') return t('group.ExploreGroups.accept')
  return t('group.ExploreGroups.join')
}
const handle = (g: ExploreGroup) => g.alias || g.domain

watch(sessionEpoch, () => { void load(true) })
onMounted(() => {
  void load(true)
  if (typeof IntersectionObserver !== 'undefined') {
    observer = new IntersectionObserver(entries => { if (entries.some(e => e.isIntersecting) && !error.value) void load() }, { rootMargin: '600px' })
    watch(sentinel, el => { observer?.disconnect(); if (el) observer?.observe(el) }, { immediate: true })
  }
})
onBeforeUnmount(() => { controller?.abort(); observer?.disconnect() })
</script>

<template>
  <section class="explore-groups" :aria-busy="loading">
    <ul class="explore-groups__list">
      <li v-for="g in groups" :key="g.id" class="explore-group" data-testid="explore-group">
        <GroupAvatar :name="g.name" :url="g.avatar_url" :size="48" />
        <div class="explore-group__body">
          <div class="explore-group__head">
            <div class="explore-group__titles">
              <div class="explore-group__name">{{ g.name }}</div>
              <div class="explore-group__handle">{{ handle(g) }}<span v-if="!g.local" class="explore-group__remote">· {{ $t('group.ExploreGroups.remote') }}</span></div>
            </div>
            <t-button size="small" shape="round" :theme="g.membership === 'join' ? 'default' : 'primary'" :variant="g.membership === 'join' ? 'outline' : 'base'"
              :loading="joining[g.id] === 'busy'" :disabled="joining[g.id] === 'requested' || joining[g.id] === 'joining' || g.membership === 'knock'"
              class="explore-group__join" @click="join(g)"
>
{{ label(g) }}
</t-button>
          </div>
          <div class="explore-group__meta">{{ $t('group.ExploreGroups.members', [g.member_count]) }}</div>
          <p v-if="g.topic" class="explore-group__topic">{{ g.topic }}</p>
          <p v-if="joining[g.id] && !['busy', 'requested', 'joining'].includes(joining[g.id]!)" class="explore-group__error" role="alert">{{ joining[g.id] }}</p>
        </div>
      </li>
    </ul>
    <p v-if="!loading && !error && done && !groups.length" class="explore-groups__empty">{{ $t('group.ExploreGroups.empty') }}</p>
    <div v-if="error" class="explore-groups__empty" role="alert">{{ error }} <t-button size="small" variant="text" @click="load(!groups.length)">{{ $t('group.ExploreGroups.retry') }}</t-button></div>
    <div v-if="loading" class="explore-groups__empty">{{ $t('group.ExploreGroups.loading') }}</div>
    <div ref="sentinel" class="explore-groups__sentinel" aria-hidden="true" />
  </section>
</template>

<style scoped>
.explore-groups__list { list-style: none; margin: 0; padding: 0; }
.explore-group { display: flex; gap: 12px; padding: 12px 16px; border-bottom: 1px solid var(--color-separator); }
.explore-group__body { flex: 1; min-width: 0; }
.explore-group__head { display: flex; align-items: flex-start; gap: 8px; }
.explore-group__titles { flex: 1; min-width: 0; }
.explore-group__name { font-weight: 700; color: var(--color-label); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.explore-group__handle, .explore-group__meta { color: var(--color-label-secondary); font-size: var(--text-footnote); overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.explore-group__remote { margin-left: 4px; }
.explore-group__join { flex: none; }
.explore-group__topic { margin: 6px 0 0; color: var(--color-label); font-size: 14px; line-height: 1.5; display: -webkit-box; -webkit-line-clamp: 3; line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; overflow-wrap: anywhere; }
.explore-group__error { margin: 6px 0 0; color: var(--color-danger, #e5484d); font-size: 13px; }
.explore-groups__empty { padding: 24px 16px; text-align: center; color: var(--color-label-secondary); font-size: 14px; }
.explore-groups__sentinel { height: 1px; }
</style>
