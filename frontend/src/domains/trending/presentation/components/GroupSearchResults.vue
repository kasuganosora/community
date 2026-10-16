<script setup lang="ts">
import { computed, onBeforeUnmount, reactive, ref, watch } from 'vue'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { groupApi } from '@/domains/group/infrastructure/groupApi'
import type { Group } from '@/domains/group/domain/group'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { isGroupReference } from '../../domain/groupSearch'
import GroupSearchCard from './GroupSearchCard.vue'

import { t } from '@/shared/i18n'
const props = defineProps<{ query: string; refreshKey?: number; via?: string[]; previewRef?: string }>()
const auth = useAuthStore()
type Source = 'joined' | 'local' | 'remote'
const scope = ref<'all' | Source>('all')
const serverInput = ref('')
const activeServer = ref('')
const serverError = ref('')
interface ResultSet { groups: Group[]; next: string | null; loading: boolean; loaded: boolean; error: string; source: string }
const empty = (): ResultSet => ({ groups: [], next: null, loading: false, loaded: false, error: '', source: '' })
const results = reactive<Record<Source, ResultSet>>({ joined: empty(), local: empty(), remote: empty() })
const sourceNames: Record<Source, string> = { joined: t('trending.GroupSearchResults.s9'), local: t('trending.GroupSearchResults.s10'), remote: t('trending.GroupSearchResults.s25') }
const visibleSources = computed<Source[]>(() => scope.value === 'all' ? ['joined', 'local'] : [scope.value])
const precise = computed(() => isGroupReference(props.query))
const resolved = ref<Awaited<ReturnType<typeof groupApi.resolve>> | null>(null)
const resolveBusy = ref(false)
const resolveError = ref('')
const controllers = new Map<string, AbortController>()
let generation = 0

function visibleGroups(source: Source): Group[] {
  if (scope.value !== 'all' || source !== 'local') return results[source].groups
  const mine = new Set(results.joined.groups.map(group => group.id))
  return results.local.groups.filter(group => !mine.has(group.id))
}

function cancel() {
  generation++
  controllers.forEach(controller => controller.abort())
  controllers.clear()
}

async function load(source: Source, more = false) {
  if (!auth.isAuthenticated || (source === 'remote' && !activeServer.value)) return
  const current = generation
  const query = props.query.trim()
  const entry = results[source]
  if (entry.loading || (more && !entry.next)) return
  const cursor = more ? entry.next || undefined : undefined
  controllers.get(source)?.abort()
  const controller = new AbortController()
  controllers.set(source, controller)
  entry.loading = true
  entry.error = ''
  try {
    const page = await groupApi.search({ q: query, scope: source, server: source === 'remote' ? activeServer.value : undefined, cursor, limit: 20 }, controller.signal)
    if (current !== generation || controller.signal.aborted) return
    const unique = new Map((more ? entry.groups : []).map(group => [group.id, group]))
    for (const group of page.groups) unique.set(group.id, group)
    entry.groups = [...unique.values()]
    entry.next = page.next_cursor && page.next_cursor !== cursor ? page.next_cursor : null
    entry.source = page.source
    entry.loaded = true
  } catch (error) {
    if (current !== generation || controller.signal.aborted) return
    entry.error = error instanceof Error ? error.message : t('trending.GroupSearchResults.s26')
  } finally {
    if (current === generation && !controller.signal.aborted) entry.loading = false
  }
}

async function resolveReference() {
  const current = generation
  const controller = new AbortController()
  controllers.set('resolve', controller)
  resolveBusy.value = true
  resolveError.value = ''
  try {
    const value = await groupApi.resolve(props.query.trim(), controller.signal, props.via, props.previewRef)
    if (current === generation && !controller.signal.aborted) resolved.value = value
  } catch (error) {
    if (current === generation && !controller.signal.aborted) resolveError.value = error instanceof Error ? error.message : t('trending.GroupSearchResults.s27')
  } finally {
    if (current === generation && !controller.signal.aborted) resolveBusy.value = false
  }
}

function refresh() {
  cancel()
  for (const source of ['joined', 'local', 'remote'] as const) results[source] = empty()
  resolved.value = null
  resolveBusy.value = false
  resolveError.value = ''
  if (!auth.isAuthenticated) return
  if (precise.value) void resolveReference()
  else for (const source of visibleSources.value) void load(source)
}

function searchRemote() {
  const input = serverInput.value.trim()
  serverError.value = ''
  // Server discovery and network policy are enforced by the backend. Reject
  // pasted URLs here so users see the expected server-name format immediately.
  if (!input || /[\s/@?#]/.test(input) || input.includes('://')) {
    serverError.value = t('trending.GroupSearchResults.s28')
    return
  }
  activeServer.value = input
  refresh()
}

watch(() => [props.query, props.refreshKey, props.via, props.previewRef, scope.value, auth.isAuthenticated, sessionEpoch.value], refresh, { immediate: true })
watch(serverInput, value => {
  // Editing the server must not keep sending subsequent keywords to the old
  // directory while the field displays a different destination.
  if (activeServer.value && value.trim() !== activeServer.value) {
    cancel()
    activeServer.value = ''
    results.remote = empty()
  }
})
onBeforeUnmount(cancel)
</script>

<template>
  <section class="group-search" :aria-label="$t('trending.GroupSearchResults.s1')">
    <div v-if="!auth.isAuthenticated" class="group-search__notice">
      <h2>{{ $t('trending.GroupSearchResults.s2') }}</h2>
      <p>{{ $t('trending.GroupSearchResults.s3') }}</p>
      <router-link class="btn btn--primary" :to="{ path: '/login', query: { redirect: $route.fullPath } }">{{ $t('trending.GroupSearchResults.s4') }}</router-link>
    </div>
    <template v-else-if="precise">
      <p v-if="resolveBusy" role="status">{{ $t('trending.GroupSearchResults.s5') }}</p>
      <div v-else-if="resolveError" class="group-search__error" role="alert">
        <p>{{ resolveError }}</p>
        <button class="btn btn--secondary btn--sm" type="button" @click="refresh">{{ $t('trending.GroupSearchResults.s6') }}</button>
      </div>
      <GroupSearchCard v-else-if="resolved" :group="resolved.group" :target="resolved.target" :via="resolved.via" :client-url="resolved.client_url" resolved />
    </template>
    <template v-else>
      <div class="group-search__filters">
        <label for="group-search-scope">{{ $t('trending.GroupSearchResults.s7') }}</label>
        <select id="group-search-scope" v-model="scope">
          <option value="all">{{ $t('trending.GroupSearchResults.s8') }}</option>
          <option value="joined">{{ $t('trending.GroupSearchResults.s9') }}</option>
          <option value="local">{{ $t('trending.GroupSearchResults.s10') }}</option>
          <option value="remote">{{ $t('trending.GroupSearchResults.s11') }}</option>
        </select>
        <form v-if="scope === 'remote'" class="group-search__server" @submit.prevent="searchRemote">
          <label for="group-search-server">{{ $t('trending.GroupSearchResults.s12') }}</label>
          <div class="group-search__server-control">
            <input id="group-search-server" v-model="serverInput" :placeholder="$t('trending.GroupSearchResults.s13')" autocomplete="off" spellcheck="false" :aria-invalid="!!serverError" :aria-describedby="serverError ? 'group-server-error' : 'group-search-privacy'" />
            <button class="btn btn--primary" type="submit">{{ $t('trending.GroupSearchResults.s14') }}</button>
          </div>
          <p v-if="serverError" id="group-server-error" class="group-search__error-text" role="alert">{{ serverError }}</p>
          <p id="group-search-privacy" class="group-search__explanation">{{ $t('trending.GroupSearchResults.s15') }}</p>
        </form>
        <p v-else class="group-search__explanation">{{ $t('trending.GroupSearchResults.s16') }}</p>
      </div>

      <section v-for="source in visibleSources" :key="source" class="group-search__section" :aria-label="sourceNames[source]" :aria-busy="results[source].loading">
        <h2>{{ sourceNames[source] }}</h2>
        <p v-if="source === 'remote' && !activeServer" class="group-search__explanation">{{ $t('trending.GroupSearchResults.s17') }}</p>
        <template v-else>
          <p v-if="results[source].source" class="group-search__source">{{ source === 'joined' ? $t('trending.GroupSearchResults.s18') : $t('trending.GroupSearchResults.s19', [results[source].source]) }}</p>
          <div class="group-search__list">
            <GroupSearchCard v-for="group in visibleGroups(source)" :key="group.id" :group="group" />
          </div>
          <p v-if="results[source].loading" role="status">{{ $t('trending.GroupSearchResults.s20') }}</p>
          <div v-else-if="results[source].error" class="group-search__error" role="alert">
            <p>{{ results[source].error }}</p>
            <button type="button" class="btn btn--secondary btn--sm" @click="load(source, results[source].loaded)">{{ $t('trending.GroupSearchResults.s21', [sourceNames[source]]) }}</button>
          </div>
          <p v-else-if="results[source].loaded && visibleGroups(source).length === 0" class="group-search__explanation">
            {{ source === 'local' && results.local.groups.length ? $t('trending.GroupSearchResults.s22') : $t('trending.GroupSearchResults.s23') }}
          </p>
          <button v-if="results[source].next && !results[source].error" class="btn btn--secondary group-search__more" type="button" :disabled="results[source].loading" @click="load(source, true)">{{ $t('trending.GroupSearchResults.s24', [sourceNames[source]]) }}</button>
        </template>
      </section>
    </template>
  </section>
</template>

<style scoped>
.group-search { padding: 1.25rem var(--gutter) 2rem; }
.group-search__filters { padding: 1rem; border-radius: var(--radius-lg); background: var(--color-bg-row); border: 1px solid var(--color-separator); }
.group-search__filters label { display: block; font-size: 0.875rem; line-height: 1.5; font-weight: 600; margin-bottom: 0.375rem; }
.group-search :is(select, input) { width: 100%; min-width: 0; min-height: 2.75rem; border: 1px solid var(--color-separator); border-radius: var(--radius-sm); background: var(--color-bg); color: var(--color-label); font: inherit; padding: 0.625rem 0.75rem; }
.group-search :is(select, input, button, a):focus-visible { outline: 2px solid var(--color-focus-ring); outline-offset: 3px; }
.group-search__server { margin-top: 1rem; }
.group-search__server-control { display: flex; align-items: stretch; gap: 0.5rem; }
.group-search__server-control button { flex-shrink: 0; }
.group-search__explanation { margin: 0.75rem 0 0; color: var(--color-label-secondary); font-size: 0.875rem; line-height: 1.6; overflow-wrap: anywhere; }
.group-search__section { margin-top: 1.5rem; }
.group-search h2 { font-size: 1rem; font-weight: 600; line-height: 1.4; margin: 0 0 0.75rem; }
.group-search__source { margin: -0.25rem 0 0.75rem; font-size: 0.8125rem; color: var(--color-label-secondary); overflow-wrap: anywhere; }
.group-search__list { display: grid; gap: 0.75rem; }
.group-search__error, .group-search__notice { border: 1px solid var(--color-separator); background: var(--color-bg-row); border-radius: var(--radius-lg); padding: 1rem; margin-top: 0.75rem; overflow-wrap: anywhere; }
.group-search__notice p, .group-search__error p { margin: 0 0 0.75rem; font-size: 0.9375rem; line-height: 1.6; }
.group-search__error-text { color: var(--color-danger); font-size: 0.875rem; }
.group-search__more { margin-top: 0.875rem; }
.group-search .btn { min-height: 2.75rem; }
@media (max-width: 360px) { .group-search__server-control { flex-direction: column; } }
</style>
