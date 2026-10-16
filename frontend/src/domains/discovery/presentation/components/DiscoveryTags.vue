<script setup lang="ts">
import { ref, onMounted, onBeforeUnmount, watch, computed } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { formatCount } from '@/shared/utils/format'
import { discoveryApi, isDiscoveryReset, type TagsPage, type TagItem } from '../../infrastructure/discoveryApi'
import { discoveryInvalidation, invalidateDiscovery } from '../composables/discoveryState'

import { t } from '@/shared/i18n'
const props = defineProps<{ compact?: boolean }>()
const auth = useAuthStore()
const windowSize = ref<'24h' | '7d'>('24h')
const items = ref<TagItem[]>([]), page = ref<TagsPage | null>(null)
const loading = ref(false), error = ref(''), reset = ref(false)
const title = computed(() => page.value?.kind === 'recent' ? t('discovery.DiscoveryTags.s17') : t('discovery.DiscoveryTags.s18'))
let version = 0, controller: AbortController | undefined
async function load(more = false) {
  if (more && (loading.value || !page.value?.next_cursor || reset.value)) return
  const mine = ++version, epoch = sessionEpoch.value
  controller?.abort(); controller = new AbortController()
  loading.value = true; error.value = ''; reset.value = false
  const cursor = more ? page.value?.next_cursor ?? undefined : undefined
  if (!more) { items.value = []; page.value = null }
  try {
    const value = await discoveryApi.tags({ window: windowSize.value, scope: 'federated', limit: props.compact ? 5 : 20, cursor }, controller.signal)
    if (mine !== version || epoch !== sessionEpoch.value) return
    if (value.disabled) { page.value = value; items.value = []; return }
    if (cursor && value.next_cursor === cursor) throw new Error(t('discovery.DiscoveryTags.s19'))
    const names = new Set(items.value.map(item => item.name))
    items.value = [...items.value, ...value.items.filter(item => {
      if (names.has(item.name)) return false
      names.add(item.name)
      return true
    })].slice(0, 200)
    if (items.value.length >= 200) value.next_cursor = null
    page.value = value
  } catch (e) {
    if (mine !== version || epoch !== sessionEpoch.value) return
    if (isDiscoveryReset(e)) reset.value = true
    else error.value = e instanceof Error ? e.message : t('discovery.DiscoveryTags.s20')
  } finally { if (mine === version) loading.value = false }
}
async function hideTag(tag: TagItem) {
  const epoch = sessionEpoch.value
  try {
    const value = await discoveryApi.addFeedback({ action: 'hide_tag', target_kind: 'tag', target_key: tag.name })
    if (epoch !== sessionEpoch.value) return
    items.value = items.value.filter(item => item.name !== tag.name)
    invalidateDiscovery(value)
    MessagePlugin.success(t('discovery.DiscoveryTags.s21'))
  } catch (e) { MessagePlugin.error(e instanceof Error ? e.message : t('discovery.DiscoveryTags.s22')) }
}
function resume() { if (!document.hidden) void load() }
watch([windowSize, sessionEpoch, discoveryInvalidation], () => { void load() }, { flush: 'sync' })
onMounted(() => { void load(); document.addEventListener('visibilitychange', resume) })
onBeforeUnmount(() => { ++version; controller?.abort(); document.removeEventListener('visibilitychange', resume) })
</script>

<template>
  <section v-if="!compact || !page?.disabled" class="discovery-tags" :class="{ 'discovery-tags--compact': compact }" :aria-busy="loading">
    <header class="discovery-tags__header">
      <h2>{{ title }}</h2>
      <select v-if="!compact" v-model="windowSize" class="discovery-tags__window" :aria-label="$t('discovery.DiscoveryTags.s1')"><option value="24h">{{ $t('discovery.DiscoveryTags.s2') }}</option><option value="7d">{{ $t('discovery.DiscoveryTags.s3') }}</option></select>
    </header>
    <p v-if="page?.disabled" class="discovery-tags__message" role="status">{{ $t('discovery.DiscoveryTags.s4') }}</p>
    <p v-else-if="page?.kind === 'recent' && !compact" class="discovery-tags__message">{{ $t('discovery.DiscoveryTags.s5') }}</p>
    <ul v-if="items.length" class="discovery-tags__list">
      <li v-for="tag in items" :key="tag.name">
        <router-link :to="`/tags/${encodeURIComponent(tag.name.replace(/^#/, ''))}`">
          <strong>#{{ tag.name }}</strong>
          <span>{{ $t('discovery.DiscoveryTags.s6', [formatCount(tag.posts_count), formatCount(tag.authors_count)]) }}</span>
        </router-link>
        <button v-if="auth.currentUser && !compact" type="button" class="discovery-tags__hide" :aria-label="$t('discovery.DiscoveryTags.s7', [tag.name])" @click="hideTag(tag)"><t-icon name="close" :size="16" /></button>
      </li>
    </ul>
    <p v-if="error" class="discovery-tags__message discovery-tags__error" role="alert">{{ error }} <button type="button" :disabled="loading" @click="load(!!items.length)">{{ $t('discovery.DiscoveryTags.s8') }}</button></p>
    <p v-if="loading" class="discovery-tags__message" role="status">{{ $t('discovery.DiscoveryTags.s9') }}</p>
    <p v-else-if="!items.length && !error && !page?.disabled && !reset" class="discovery-tags__message">{{ $t('discovery.DiscoveryTags.s10') }}</p>
    <p v-if="reset" class="discovery-tags__message" role="status">{{ $t('discovery.DiscoveryTags.s11') }} <button type="button" @click="load()">{{ $t('discovery.DiscoveryTags.s12') }}</button></p>
    <div v-if="!compact && page && !page.disabled" class="discovery-tags__footer">
      <p v-if="!page.coverage_complete">{{ $t('discovery.DiscoveryTags.s13') }}</p>
      <button v-if="page.next_cursor && !reset" type="button" class="btn btn--secondary btn--sm" :disabled="loading" @click="load(true)">{{ $t('discovery.DiscoveryTags.s14') }}</button>
      <button v-else type="button" class="btn btn--ghost btn--sm" :disabled="loading" @click="load()">{{ $t('discovery.DiscoveryTags.s15') }}</button>
    </div>
    <router-link v-if="compact" class="discovery-tags__more" to="/explore/topics">{{ $t('discovery.DiscoveryTags.s16') }}</router-link>
  </section>
</template>

<style scoped>
.discovery-tags { min-width: 0; }
.discovery-tags--compact { background: var(--color-bg-grouped); border-radius: var(--radius-lg); overflow: hidden; }
.discovery-tags__header, .discovery-tags__message, .discovery-tags__footer { padding: var(--space-3) var(--space-4); }
.discovery-tags__header { display: flex; align-items: center; justify-content: space-between; gap: var(--space-3); }
.discovery-tags__header h2 { font-size: var(--text-title-3); font-weight: var(--weight-bold); }
.discovery-tags__message, .discovery-tags__footer { color: var(--color-label-secondary); font-size: var(--text-footnote); line-height: 1.6; }
.discovery-tags__window { flex: none; min-height: 2rem; border: none; border-radius: var(--radius-full); background: var(--color-fill); color: var(--color-label); padding: .25rem 1.75rem .25rem .75rem; font: inherit; font-size: var(--text-footnote); appearance: none; -webkit-appearance: none; background-image: linear-gradient(45deg, transparent 50%, currentColor 50%), linear-gradient(135deg, currentColor 50%, transparent 50%); background-position: calc(100% - .9rem) 55%, calc(100% - .6rem) 55%; background-size: .3rem .3rem; background-repeat: no-repeat; cursor: pointer; }
@media (pointer: coarse) { .discovery-tags__window { font-size: 16px; } }
.discovery-tags__list { list-style: none; padding: 0; }
.discovery-tags__list li { display: flex; align-items: center; padding: 0 var(--space-4); gap: .5rem; }
.discovery-tags__list a { display: block; flex: 1; min-width: 0; color: var(--color-label); padding: var(--space-3) 0; text-decoration: none; }
.discovery-tags__list strong { display: block; font-size: var(--text-body); overflow-wrap: anywhere; }
.discovery-tags__list span, .discovery-tags__list small { display: block; margin-top: .25rem; color: var(--color-label-secondary); font-size: var(--text-footnote); }
.discovery-tags__hide { width: var(--hit-target); height: var(--hit-target); flex: none; color: var(--color-label-secondary); }
.discovery-tags__hide:active:not(:focus-visible) { background: var(--color-pressed); border-radius: var(--radius-full); }
.discovery-tags__more { display: flex; align-items: center; min-height: var(--hit-target); padding: var(--space-2) var(--space-4) var(--space-3); color: var(--color-accent-text); }
.discovery-tags__message button { color: var(--color-accent-text); min-height: var(--hit-target); }
.discovery-tags__error { color: var(--color-danger); }
.discovery-tags__footer { display: flex; flex-wrap: wrap; gap: .5rem 1rem; align-items: center; }
.discovery-tags__footer p { flex-basis: 100%; }
.discovery-tags__footer button { min-height: var(--hit-target); }
.discovery-tags :is(a, button, select):focus-visible { outline: 2px solid var(--color-accent); outline-offset: 3px; }
@media (hover: hover) and (pointer: fine) { .discovery-tags__list li:hover { background: var(--color-hover); } }
</style>
