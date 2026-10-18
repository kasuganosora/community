<script setup lang="ts">
// Shows the search index backfill (e.g. after a Misskey/Mastodon migration)
// until every index has caught up. Polls only while a rebuild is running.
import { computed, onBeforeUnmount, onMounted, ref } from 'vue'
import { useI18n } from 'vue-i18n'
import { discoveryAdminApi, type SearchIndexProgress } from '../../infrastructure/discoveryAdminApi'

const { t, locale } = useI18n()
const items = ref<SearchIndexProgress[]>([])
const now = ref(Date.now())
let timer: ReturnType<typeof setTimeout> | undefined
let clock: ReturnType<typeof setInterval> | undefined
let alive = true

const running = computed(() => items.value.filter(i => i.running && !i.finished))
const done = computed(() => running.value.reduce((n, i) => n + i.done, 0))
const total = computed(() => running.value.reduce((n, i) => n + i.total, 0))
const percent = computed(() => total.value ? Math.min(100, (done.value / total.value) * 100) : 0)
const updatedAgo = computed(() => {
  const latest = Math.max(...running.value.map(i => Date.parse(i.updated_at) || 0))
  return latest > 0 ? Math.max(0, Math.round((now.value - latest) / 1000)) : null
})
function indexName(name: string) { return t(`admin.SearchIndexBanner.index_${name}`) }
function fmt(n: number) { return n.toLocaleString(locale.value) }

async function refresh() {
  try {
    const status = await discoveryAdminApi.status()
    if (!alive) return
    items.value = Array.isArray(status.search_index) ? status.search_index : []
  } catch {
    if (!alive) return
    items.value = [] // not permitted or unavailable: stay silent
    return
  }
  if (running.value.length && alive) timer = setTimeout(refresh, 5000)
}
onMounted(() => { void refresh(); clock = setInterval(() => { now.value = Date.now() }, 1000) })
onBeforeUnmount(() => { alive = false; clearTimeout(timer); clearInterval(clock) })
</script>

<template>
  <section v-if="running.length" class="search-index-banner" role="status" aria-live="polite" data-testid="search-index-banner">
    <div class="search-index-banner__head">
      <strong>{{ t('admin.SearchIndexBanner.title') }}</strong>
      <span>{{ t('admin.SearchIndexBanner.percent', { n: percent.toFixed(percent < 10 ? 1 : 0) }) }}</span>
    </div>
    <p class="search-index-banner__hint">{{ t('admin.SearchIndexBanner.hint') }}</p>
    <div class="search-index-banner__bar" role="progressbar" aria-valuemin="0" aria-valuemax="100" :aria-valuenow="Math.round(percent)" :aria-label="t('admin.SearchIndexBanner.title')">
      <span :style="{ width: `${percent}%` }" />
    </div>
    <ul class="search-index-banner__items">
      <li v-for="i in running" :key="i.index">{{ indexName(i.index) }} <b>{{ fmt(i.done) }} / {{ fmt(i.total) }}</b></li>
      <li v-if="updatedAgo !== null" class="search-index-banner__ago">{{ t('admin.SearchIndexBanner.updatedAgo', { n: updatedAgo }) }}</li>
    </ul>
  </section>
</template>

<style scoped>
.search-index-banner { border: 1px solid color-mix(in srgb, var(--td-brand-color, #4f6bed) 35%, transparent); background: color-mix(in srgb, var(--td-brand-color, #4f6bed) 8%, transparent); border-radius: 12px; padding: 12px 16px; margin-bottom: 16px; display: flex; flex-direction: column; gap: 8px; }
.search-index-banner__head { display: flex; justify-content: space-between; gap: 12px; }
.search-index-banner__head span { font-variant-numeric: tabular-nums; }
.search-index-banner__hint { margin: 0; font-size: 13px; opacity: .8; }
.search-index-banner__bar { height: 8px; border-radius: 999px; background: color-mix(in srgb, var(--td-brand-color, #4f6bed) 16%, transparent); overflow: hidden; }
.search-index-banner__bar span { display: block; height: 100%; background: var(--td-brand-color, #4f6bed); border-radius: inherit; transition: width .4s ease; }
.search-index-banner__items { list-style: none; margin: 0; padding: 0; display: flex; flex-wrap: wrap; gap: 4px 16px; font-size: 13px; font-variant-numeric: tabular-nums; }
.search-index-banner__ago { opacity: .7; }
@media (prefers-reduced-motion: reduce) { .search-index-banner__bar span { transition: none; } }
</style>
