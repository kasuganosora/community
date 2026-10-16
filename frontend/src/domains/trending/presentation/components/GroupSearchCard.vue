<script setup lang="ts">
import { computed, onBeforeUnmount, ref, watch } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { groupPath, type Group, type GroupJoinResult } from '@/domains/group/domain/group'
import { groupApi } from '@/domains/group/infrastructure/groupApi'
import { isRetryableGroupRequestError } from '@/domains/group/infrastructure/groupErrors'
import GroupAvatar from '@/domains/group/presentation/components/GroupAvatar.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { groupClientLink, groupMembershipLabel } from '../../domain/groupSearch'

import { t } from '@/shared/i18n'
const props = defineProps<{ group?: Group; target?: string; via?: string[]; source?: string; resolved?: boolean; clientUrl?: string }>()
const auth = useAuthStore()
const current = ref<Group>()
const resultMembership = ref('')
const busy = ref(false)
const error = ref('')
const status = ref('')
const externalClient = ref('')
const clientRequired = ref(false)
const name = computed(() => current.value?.name || t('trending.GroupSearchCard.s14'))
const membership = computed(() => resultMembership.value || current.value?.membership || 'none')
const clientLink = computed(() => groupClientLink(externalClient.value || current.value?.client_url || props.clientUrl))
const target = computed(() => props.target || current.value?.target || current.value?.id || '')
const needsClient = computed(() => clientRequired.value || (membership.value === 'join' && current.value?.encryption === 'encrypted'))
const canJoin = computed(() => auth.isAuthenticated && !!target.value && !clientRequired.value && !['join', 'knock', 'ban'].includes(membership.value) && (membership.value === 'invite' || current.value?.join_rule !== 'invite'))
const actionLabel = computed(() => {
  if (membership.value === 'invite') return t('trending.GroupSearchCard.s15')
  if (current.value?.join_rule?.startsWith('knock')) return t('trending.GroupSearchCard.s16')
  if (current.value?.encryption === 'encrypted') return t('trending.GroupSearchCard.s17')
  return t('trending.GroupSearchCard.s18')
})
let active = true
let version = 0
let operationID = ''
let controller: AbortController | undefined
let timer: ReturnType<typeof setTimeout> | undefined
let deadline: ReturnType<typeof setTimeout> | undefined
let polls = 0

function cancel() {
  version++
  controller?.abort()
  if (timer) clearTimeout(timer)
  clearTimeout(deadline)
  timer = undefined
}
function reset() {
  cancel()
  current.value = props.group
  resultMembership.value = ''; busy.value = false; error.value = ''; status.value = ''
  externalClient.value = ''; clientRequired.value = false; operationID = ''; polls = 0
}
function valid(attempt: number) { return active && version === attempt && auth.isAuthenticated }
function fail(reason: unknown, attempt: number) {
  if (!valid(attempt)) return
  clearTimeout(timer); clearTimeout(deadline)
  busy.value = false
  status.value = ''
  error.value = reason instanceof Error ? reason.message : t('trending.GroupSearchCard.s19')
}
function startDeadline(attempt: number) {
  clearTimeout(deadline)
  deadline = setTimeout(() => {
    if (!valid(attempt)) return
    fail(new Error(operationID ? t('trending.GroupSearchCard.s20') : t('trending.GroupSearchCard.s21')), attempt)
    controller?.abort()
    version++
  }, 120000)
}
function finish(result: GroupJoinResult, attempt: number) {
  if (!valid(attempt)) return
  if (result.client_required) {
    if (result.group) current.value = result.group
    externalClient.value = result.client_url || ''
    clientRequired.value = true
    status.value = t('trending.GroupSearchCard.s22')
    clearTimeout(deadline)
    busy.value = false; operationID = ''
    return
  }
  if (result.status === 'joining') {
    operationID = result.operation_id || operationID
    status.value = t('trending.GroupSearchCard.s23')
    if (!operationID) { fail(new Error(t('trending.GroupSearchCard.s24')), attempt); return }
    if (polls >= 80) { fail(new Error(t('trending.GroupSearchCard.s20')), attempt); return }
    timer = setTimeout(() => { void poll(attempt) }, 1500)
    return
  }
  if (result.group) current.value = result.group
  resultMembership.value = result.status === 'knock' ? 'knock' : 'join'
  status.value = result.status === 'knock' ? t('trending.GroupSearchCard.s25') : current.value?.encryption === 'encrypted' ? t('trending.GroupSearchCard.s26') : t('trending.GroupSearchCard.s27')
  clearTimeout(deadline)
  busy.value = false; operationID = ''
}
async function poll(attempt: number) {
  if (!valid(attempt)) return
  controller = new AbortController()
  polls++
  try { finish(await groupApi.joinStatus(operationID, controller.signal), attempt) }
  catch (reason) {
    if (!valid(attempt)) return
    if (isRetryableGroupRequestError(reason)) {
      status.value = t('trending.GroupSearchCard.s28')
      timer = setTimeout(() => { void poll(attempt) }, 3000)
      return
    }
    fail(reason, attempt)
  }
}
async function join() {
  if (busy.value || !canJoin.value) return
  const attempt = ++version
  busy.value = true; error.value = ''; status.value = ''
  controller = new AbortController()
  startDeadline(attempt)
  let action: 'join' | 'knock' = membership.value !== 'invite' && current.value?.join_rule?.startsWith('knock') ? 'knock' : 'join'
  let joinTarget = target.value
  let via = props.via || current.value?.via || []
  try {
    // A directory preview is signed for this user and room. Keep its routing
    // hints and validate it before the explicit mutation; search itself is read-only.
    if (!props.resolved) {
      const preview = await groupApi.resolve(joinTarget, controller.signal, via, current.value?.preview_ref)
      if (!valid(attempt)) return
      joinTarget = preview.target
      via = [...new Set([...via, ...preview.via])]
      externalClient.value = preview.client_url || ''
      if (preview.group) {
        current.value = preview.group
        if (!canJoin.value) { busy.value = false; clearTimeout(deadline); return }
        action = preview.group.membership !== 'invite' && preview.group.join_rule?.startsWith('knock') ? 'knock' : 'join'
      }
    }
    finish(await groupApi.join({ target: joinTarget, via, action }, controller.signal), attempt)
  } catch (reason) { fail(reason, attempt) }
}
function retry() {
  if (busy.value) return
  if (!operationID) { void join(); return }
  const attempt = ++version
  busy.value = true; error.value = ''; polls = 0
  startDeadline(attempt)
  void poll(attempt)
}
watch(() => [props.group, props.target, props.via, props.clientUrl, auth.isAuthenticated, sessionEpoch.value], reset, { immediate: true, flush: 'sync' })
onBeforeUnmount(() => { active = false; cancel() })
</script>

<template>
  <article class="group-result" :aria-label="name" :aria-busy="busy">
    <header class="group-result__header">
      <GroupAvatar :name="name" :url="current?.avatar_url" :size="44" />
      <div class="group-result__heading">
        <h3>{{ name }}</h3>
        <div v-if="groupMembershipLabel(membership) || current" class="group-result__meta">
          <span v-if="groupMembershipLabel(membership)">{{ groupMembershipLabel(membership) }}</span>
          <span v-if="typeof current?.member_count === 'number' && current.member_count >= 0">{{ $t('trending.GroupSearchCard.s1', [current.member_count.toLocaleString()]) }}</span>
          <span v-if="current?.encryption === 'encrypted'">{{ $t('trending.GroupSearchCard.s2') }}</span>
          <span v-else-if="current?.encryption === 'unencrypted'">{{ $t('trending.GroupSearchCard.s3') }}</span>
          <span v-else-if="current">{{ $t('trending.GroupSearchCard.s4') }}</span>
        </div>
      </div>
    </header>
    <p v-if="current?.topic" class="group-result__topic">{{ current.topic }}</p>
    <p v-if="!current" class="group-result__hint">{{ $t('trending.GroupSearchCard.s5') }}</p>
    <p v-else-if="current.encryption === 'encrypted' && !status" class="group-result__hint">{{ $t('trending.GroupSearchCard.s6') }}</p>
    <p v-if="source" class="group-result__hint">{{ source }}</p>
    <p v-if="membership !== 'invite' && current?.join_rule === 'invite' && membership === 'none'" class="group-result__hint">{{ $t('trending.GroupSearchCard.s7') }}</p>
    <p v-if="status" class="group-result__status" role="status">{{ status }}</p>
    <p v-if="error" class="group-result__error" role="alert">{{ error }}</p>
    <div v-if="error || canJoin || (needsClient && clientLink) || (!needsClient && membership === 'join' && current)" class="group-result__actions">
      <button v-if="error" class="btn btn--secondary btn--sm" type="button" :disabled="busy" @click="retry">{{ operationID ? $t('trending.GroupSearchCard.s8') : $t('trending.GroupSearchCard.s9') }}</button>
      <a v-else-if="needsClient && clientLink" class="btn btn--primary btn--sm" :href="clientLink" target="_blank" rel="noopener noreferrer">{{ $t('trending.GroupSearchCard.s10') }}<span class="sr-only">{{ $t('trending.GroupSearchCard.s11') }}</span></a>
      <router-link v-else-if="membership === 'join' && current && !needsClient" class="btn btn--primary btn--sm" :to="groupPath(current.id)">{{ $t('trending.GroupSearchCard.s12') }}</router-link>
      <button v-else-if="canJoin" class="btn btn--primary btn--sm" type="button" :disabled="busy" @click="join">{{ busy ? $t('trending.GroupSearchCard.s13') : actionLabel }}</button>
    </div>
  </article>
</template>

<style scoped>
.group-result { display: grid; gap: 1rem; min-width: 0; padding: 1.25rem; border: 1px solid var(--color-separator); border-radius: var(--radius-lg); background: var(--color-bg-row); }
.group-result__header { display: flex; align-items: center; gap: 0.875rem; min-width: 0; }
.group-result__heading { flex: 1; min-width: 0; }
.group-result h3 { margin: 0; font-size: 1rem; font-weight: 600; line-height: 1.5; overflow-wrap: anywhere; }
.group-result__meta { display: flex; gap: 0.25rem 0.75rem; flex-wrap: wrap; margin-top: 0.25rem; color: var(--color-label-secondary); font-size: 0.8125rem; line-height: 1.5; }
.group-result__topic { margin: 0; font-size: 0.9375rem; line-height: 1.6; overflow-wrap: anywhere; white-space: pre-line; display: -webkit-box; -webkit-line-clamp: 3; -webkit-box-orient: vertical; overflow: hidden; }
.group-result__hint, .group-result__status, .group-result__error { margin: 0; font-size: 0.875rem; line-height: 1.6; overflow-wrap: anywhere; }
.group-result__hint { color: var(--color-label-secondary); }
.group-result__error { color: var(--color-danger); }
.group-result__actions { display: flex; align-items: center; min-width: 0; }
.group-result__actions .btn { min-height: 2.75rem; max-width: 100%; white-space: normal; }
.group-result__actions :is(a, button):focus-visible { outline: 2px solid var(--color-accent); outline-offset: 3px; }
@media (max-width: 480px) { .group-result { padding: 1rem; } }
</style>
