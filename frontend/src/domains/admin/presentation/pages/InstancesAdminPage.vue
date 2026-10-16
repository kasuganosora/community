<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { RouterLink } from 'vue-router'
import { federationJobsApi as api, type Instance } from '../../infrastructure/federationJobsApi'
import { moderationSettingsApi, type InstanceModerationAction } from '../../infrastructure/moderationSettingsApi'
import QueueSparkline from '../components/QueueSparkline.vue'

import { t } from '@/shared/i18n'
const PAGE = 30
const STATUSES = [
  { key: '', label: t('admin.InstancesAdminPage.s40') }, { key: 'federating', label: t('admin.InstancesAdminPage.s41') }, { key: 'subscribing', label: t('admin.InstancesAdminPage.s42') },
  { key: 'publishing', label: t('admin.InstancesAdminPage.s43') }, { key: 'failing', label: t('admin.InstancesAdminPage.s44') },
  { key: 'suspended', label: t('admin.InstancesAdminPage.s45') }, { key: 'blocked', label: t('admin.InstancesAdminPage.s46') }, { key: 'silenced', label: t('admin.InstancesAdminPage.s47') }, { key: 'media_silenced', label: t('admin.InstancesAdminPage.s48') },
]
const SORTS = [
  { key: 'pubsub_desc', label: t('admin.InstancesAdminPage.s49') }, { key: 'pubsub_asc', label: t('admin.InstancesAdminPage.s50') },
  { key: 'accounts_desc', label: t('admin.InstancesAdminPage.s51') }, { key: 'accounts_asc', label: t('admin.InstancesAdminPage.s52') },
  { key: 'last_activity_desc', label: t('admin.InstancesAdminPage.s53') }, { key: 'last_activity_asc', label: t('admin.InstancesAdminPage.s54') },
  { key: 'first_seen_desc', label: t('admin.InstancesAdminPage.s55') }, { key: 'first_seen_asc', label: t('admin.InstancesAdminPage.s56') },
]
const STATUS_LABEL: Record<string, string> = { federating: t('admin.InstancesAdminPage.s41'), subscribing: t('admin.InstancesAdminPage.s42'), publishing: t('admin.InstancesAdminPage.s43'), failing: t('admin.InstancesAdminPage.s44'), idle: t('admin.InstancesAdminPage.s57') }

const items = ref<Instance[]>([]), total = ref(0), page = ref(0), busy = ref(false), error = ref('')
const q = ref(''), status = ref(''), sort = ref('pubsub_desc')
const activity = ref<Record<string, number[]>>({})
const broken = ref<Record<string, boolean>>({})
const selected = ref<Instance | null>(null)
const sentinel = ref<HTMLElement | null>(null)
const hasMore = computed(() => items.value.length < total.value)
let version = 0, debounce: ReturnType<typeof setTimeout> | undefined, observer: IntersectionObserver | undefined

async function load(reset = false) {
  if (busy.value && !reset) return
  const mine = reset ? ++version : version
  if (reset) { page.value = 0; items.value = []; total.value = 0 }
  busy.value = true; error.value = ''
  try {
    const data = await api.instances(page.value + 1, { q: q.value.trim(), status: status.value, sort: sort.value }, PAGE)
    if (mine !== version) return
    page.value++
    items.value = [...items.value, ...(data.items || [])]
    total.value = data.total
    loadActivity((data.items || []).map(i => i.domain))
  } catch { if (mine === version) error.value = t('admin.InstancesAdminPage.s58') }
  finally { if (mine === version) busy.value = false }
}
async function loadActivity(domains: string[]) {
  if (!domains.length) return
  try { const r = await api.instanceActivity(domains); activity.value = { ...activity.value, ...r.items } } catch { /* sparkline is optional */ }
}
watch(q, () => { clearTimeout(debounce); debounce = setTimeout(() => load(true), 300) })
watch([status, sort], () => load(true))

function title(i: Instance) { return i.name?.trim() || i.domain }
function subline(i: Instance) { return [i.domain, [i.software, i.version].filter(Boolean).join(' ')].filter(Boolean).join(' / ') }
function letter(i: Instance) { return (title(i).match(/[\p{L}\p{N}]/u)?.[0] || '?').toUpperCase() }
function hue(d: string) { let h = 0; for (const c of d) h = (h * 31 + c.charCodeAt(0)) % 360; return h }
function when(v?: string | null) { return v ? new Date(v).toLocaleString() : t('admin.InstancesAdminPage.s59') }
function sum(v?: number[]) { return (v || []).reduce((a, b) => a + b, 0) }
const acting = ref(''), actionError = ref('')
const ACTION_CONFIRM: Partial<Record<InstanceModerationAction, string>> = {
  block: t('admin.InstancesAdminPage.s60'),
  suspend: t('admin.InstancesAdminPage.s61'),
  silence: t('admin.InstancesAdminPage.s62'),
}
async function moderate(i: Instance, action: InstanceModerationAction) {
  const confirmText = ACTION_CONFIRM[action]
  if (confirmText && !window.confirm(`${i.domain}\n\n${confirmText}`)) return
  acting.value = action; actionError.value = ''
  try {
    const st = await moderationSettingsApi.instanceAction(i.domain, action)
    const patch = { is_blocked: st.is_blocked, is_suspended: st.is_suspended, is_silenced: st.is_silenced, is_media_silenced: st.is_media_silenced }
    items.value = items.value.map(x => x.domain === i.domain ? { ...x, ...patch } : x)
    if (selected.value?.domain === i.domain) selected.value = { ...selected.value, ...patch }
  } catch { actionError.value = t('admin.InstancesAdminPage.s63') }
  finally { acting.value = '' }
}
function flags(i: Instance) {
  return [i.is_blocked && t('admin.InstancesAdminPage.s46'), i.is_suspended && t('admin.InstancesAdminPage.s45'), i.is_silenced && t('admin.InstancesAdminPage.s47'), i.is_media_silenced && t('admin.InstancesAdminPage.s48')].filter(Boolean) as string[]
}
function onKey(e: KeyboardEvent) { if (e.key === 'Escape') selected.value = null }

onMounted(() => {
  load(true)
  window.addEventListener('keydown', onKey)
  if (typeof IntersectionObserver !== 'undefined') {
    observer = new IntersectionObserver(e => { if (e.some(x => x.isIntersecting) && hasMore.value && !busy.value && !error.value) load() }, { rootMargin: '200px' })
    watch(sentinel, (el, old) => { if (old) observer?.unobserve(old); if (el) observer?.observe(el) }, { immediate: true })
  }
})
onBeforeUnmount(() => { clearTimeout(debounce); observer?.disconnect(); window.removeEventListener('keydown', onKey); version++ })
</script>

<template>
  <div class="admin-page inst-page">
    <header><h1>{{ $t('admin.InstancesAdminPage.s1') }}</h1><p>{{ $t('admin.InstancesAdminPage.s2') }}</p></header>

    <div class="inst-filters">
      <label class="inst-search">
        <span class="sr-only">{{ $t('admin.InstancesAdminPage.s3') }}</span>
        <svg viewBox="0 0 24 24" aria-hidden="true"><circle cx="11" cy="11" r="7" /><path d="m20 20-3.5-3.5" /></svg>
        <input v-model="q" type="search" maxlength="255" :placeholder="$t('admin.InstancesAdminPage.s3')" :aria-label="$t('admin.InstancesAdminPage.s3')" />
      </label>
      <div class="inst-selects">
        <label class="admin-field">{{ $t('admin.InstancesAdminPage.s4') }}
          <select v-model="status" :aria-label="$t('admin.InstancesAdminPage.s4')"><option v-for="s in STATUSES" :key="s.key" :value="s.key">{{ s.label }}</option></select>
        </label>
        <label class="admin-field">{{ $t('admin.InstancesAdminPage.s5') }}
          <select v-model="sort" :aria-label="$t('admin.InstancesAdminPage.s5')"><option v-for="s in SORTS" :key="s.key" :value="s.key">{{ s.label }}</option></select>
        </label>
      </div>
    </div>

    <p v-if="error" class="admin-error" role="alert">{{ error }} <button class="admin-button" @click="load(!items.length)">{{ $t('admin.InstancesAdminPage.s6') }}</button></p>
    <p v-if="!busy && !error && !items.length" class="admin-empty">{{ $t('admin.InstancesAdminPage.s7') }}</p>

    <section class="inst-grid" :aria-busy="busy" :aria-label="$t('admin.InstancesAdminPage.s8')">
      <button v-for="i in items" :key="i.domain" class="admin-card inst-card" :class="`is-${i.status || 'idle'}`" @click="selected = i">
        <span class="inst-icon" :style="{ '--h': hue(i.domain) }">
          <img v-if="i.icon_url && !broken[i.domain]" :src="i.icon_url" alt="" loading="lazy" referrerpolicy="no-referrer" @error="broken = { ...broken, [i.domain]: true }" />
          <span v-else aria-hidden="true">{{ letter(i) }}</span>
        </span>
        <span class="inst-main">
          <strong class="inst-ellipsis">{{ title(i) }}<span v-for="f in flags(i)" :key="f" class="inst-flag">{{ f }}</span></strong>
          <span class="inst-sub inst-ellipsis">{{ subline(i) }}</span>
        </span>
        <span class="inst-spark"><QueueSparkline :height="28" :label="$t('admin.InstancesAdminPage.s9', [i.domain, sum(activity[i.domain])])" :series="[{ values: activity[i.domain] || [], tone: i.status === 'failing' ? 'danger' : 'accent' }]" /></span>
      </button>
    </section>
    <div ref="sentinel" class="inst-more">
      <span v-if="busy">{{ $t('admin.InstancesAdminPage.s10') }}</span>
      <button v-else-if="hasMore && !error" class="admin-button" @click="load()">{{ $t('admin.InstancesAdminPage.s11') }}</button>
      <span v-else-if="items.length" class="inst-muted">{{ $t('admin.InstancesAdminPage.s12', [total]) }}</span>
    </div>

    <div v-if="selected" class="inst-backdrop" @click.self="selected = null">
      <aside class="inst-drawer" role="dialog" aria-modal="true" :aria-label="$t('admin.InstancesAdminPage.s13', [selected.domain])">
        <header class="inst-drawer__head">
          <span class="inst-icon inst-icon--lg" :style="{ '--h': hue(selected.domain) }">
            <img v-if="selected.icon_url && !broken[selected.domain]" :src="selected.icon_url" alt="" referrerpolicy="no-referrer" @error="broken = { ...broken, [selected.domain]: true }" />
            <span v-else aria-hidden="true">{{ letter(selected) }}</span>
          </span>
          <div class="inst-main"><h2 class="inst-ellipsis">{{ title(selected) }}</h2><span class="inst-sub">{{ subline(selected) }}</span></div>
          <button class="admin-button" :aria-label="$t('admin.InstancesAdminPage.s14')" @click="selected = null">✕</button>
        </header>
        <p class="inst-badges"><span class="inst-badge" :class="`is-${selected.status || 'idle'}`">{{ STATUS_LABEL[selected.status || 'idle'] }}</span><span v-for="f in flags(selected)" :key="f" class="inst-badge is-moderated">{{ f }}</span></p>
        <figure class="inst-chart"><figcaption>{{ $t('admin.InstancesAdminPage.s15', [sum(activity[selected.domain])]) }}</figcaption>
          <QueueSparkline :height="56" :label="$t('admin.InstancesAdminPage.s16', [selected.domain])" :series="[{ values: activity[selected.domain] || [], tone: 'accent' }]" /></figure>
        <dl class="inst-stats">
          <div><dt>{{ $t('admin.InstancesAdminPage.s17') }}</dt><dd>{{ selected.accounts }}</dd></div>
          <div><dt>{{ $t('admin.InstancesAdminPage.s18') }}</dt><dd>{{ selected.suspended_accounts }}</dd></div>
          <div><dt>{{ $t('admin.InstancesAdminPage.s19') }}</dt><dd>{{ selected.following }}</dd></div>
          <div><dt>{{ $t('admin.InstancesAdminPage.s20') }}</dt><dd>{{ selected.followers }}</dd></div>
        </dl>
        <dl class="inst-meta">
          <div><dt>{{ $t('admin.InstancesAdminPage.s21') }}</dt><dd>{{ when(selected.last_activity) }}</dd></div>
          <div><dt>{{ $t('admin.InstancesAdminPage.s22') }}</dt><dd>{{ when(selected.first_seen) }}</dd></div>
          <div><dt>{{ $t('admin.InstancesAdminPage.s23') }}</dt><dd>{{ selected.metadata_fetched_at ? when(selected.metadata_fetched_at) : $t('admin.InstancesAdminPage.s24') }}</dd></div>
          <div v-if="selected.metadata_failed_at"><dt>{{ $t('admin.InstancesAdminPage.s25') }}</dt><dd class="bad">{{ when(selected.metadata_failed_at) }}</dd></div>
        </dl>
        <div class="inst-actions">
          <a class="admin-button" :href="`https://${selected.domain}`" target="_blank" rel="noopener noreferrer">{{ $t('admin.InstancesAdminPage.s26') }}</a>
          <RouterLink class="admin-button" :to="{ path: '/admin/jobs' }">{{ $t('admin.InstancesAdminPage.s27') }}</RouterLink>
        </div>
        <section class="inst-moderation" :aria-label="$t('admin.InstancesAdminPage.s28')">
          <h3>{{ $t('admin.InstancesAdminPage.s29') }}</h3>
          <div class="inst-actions">
            <button class="admin-button" :class="{ 'is-danger': !selected.is_suspended }" :disabled="!!acting" @click="moderate(selected, selected.is_suspended ? 'unsuspend' : 'suspend')">{{ selected.is_suspended ? $t('admin.InstancesAdminPage.s30') : $t('admin.InstancesAdminPage.s31') }}</button>
            <button class="admin-button" :disabled="!!acting" @click="moderate(selected, selected.is_silenced ? 'unsilence' : 'silence')">{{ selected.is_silenced ? $t('admin.InstancesAdminPage.s32') : $t('admin.InstancesAdminPage.s33') }}</button>
            <button class="admin-button" :disabled="!!acting" @click="moderate(selected, selected.is_media_silenced ? 'media_unsilence' : 'media_silence')">{{ selected.is_media_silenced ? $t('admin.InstancesAdminPage.s34') : $t('admin.InstancesAdminPage.s35') }}</button>
            <button class="admin-button" :class="{ 'is-danger': !selected.is_blocked }" :disabled="!!acting" @click="moderate(selected, selected.is_blocked ? 'unblock' : 'block')">{{ selected.is_blocked ? $t('admin.InstancesAdminPage.s36') : $t('admin.InstancesAdminPage.s37') }}</button>
          </div>
          <p v-if="actionError" class="admin-error" role="alert">{{ actionError }}</p>
          <p class="inst-muted">{{ $t('admin.InstancesAdminPage.s38') }} <RouterLink to="/admin/moderation">{{ $t('admin.InstancesAdminPage.s29') }}</RouterLink> {{ $t('admin.InstancesAdminPage.s39') }}</p>
        </section>
      </aside>
    </div>
  </div>
</template>

<style scoped>
.inst-filters { display: flex; flex-direction: column; gap: 10px; margin-bottom: 16px; }
.inst-search { position: relative; display: block; }
.inst-search svg { position: absolute; left: 10px; top: 50%; width: 16px; height: 16px; transform: translateY(-50%); fill: none; stroke: var(--color-label-tertiary); stroke-width: 2; stroke-linecap: round; }
.inst-search input { width: 100%; box-sizing: border-box; height: 36px; padding: 0 10px 0 34px; border-radius: 8px; border: 1px solid var(--color-separator); background: var(--admin-control-bg); color: var(--color-label); font: inherit; }
.inst-selects { display: grid; grid-template-columns: 1fr 1fr; gap: 10px; }
.inst-selects select { width: 100%; }
.inst-grid { display: grid; grid-template-columns: repeat(3, minmax(0, 1fr)); gap: 10px; }
.inst-card { display: flex; align-items: center; gap: 10px; margin: 0; padding: 10px 12px; text-align: left; font: inherit; color: inherit; cursor: pointer; min-width: 0; }
.inst-card:hover, .inst-card:focus-visible { border-color: var(--admin-accent); }
.inst-card.is-failing { border-left: 3px solid var(--color-danger); }
.inst-icon { flex: none; width: 36px; height: 36px; border-radius: 8px; overflow: hidden; display: grid; place-items: center; background: hsl(var(--h) 45% 38%); color: #fff; font-weight: 700; }
.inst-icon img { width: 100%; height: 100%; object-fit: cover; background: var(--color-fill); }
.inst-icon--lg { width: 52px; height: 52px; font-size: 22px; border-radius: 12px; }
.inst-main { flex: 1; min-width: 0; display: flex; flex-direction: column; gap: 2px; }
.inst-main h2 { margin: 0; font-size: 18px; }
.inst-sub { font-family: ui-monospace, SFMono-Regular, Menlo, monospace; font-size: 11px; color: var(--color-label-secondary); }
.inst-ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.inst-spark { flex: none; width: 64px; }
.inst-more { display: flex; justify-content: center; padding: 16px 0; min-height: 24px; color: var(--color-label-secondary); }
.inst-muted { color: var(--color-label-tertiary); font-size: 12px; }
.inst-backdrop { position: fixed; inset: 0; z-index: 50; background: rgb(0 0 0 / 45%); display: flex; justify-content: flex-end; }
.inst-drawer { width: min(420px, 100%); height: 100%; overflow-y: auto; box-sizing: border-box; padding: 20px; background: var(--color-bg-grouped, var(--color-bg-row)); border-left: 1px solid var(--color-separator); display: flex; flex-direction: column; gap: 14px; }
.inst-drawer__head { display: flex; align-items: center; gap: 12px; }
.inst-badges { margin: 0; display: flex; flex-wrap: wrap; gap: 6px; }
.inst-badge.is-moderated { background: color-mix(in srgb, var(--color-danger) 14%, transparent); color: var(--color-danger); }
.inst-flag { margin-left: 6px; padding: 0 6px; border-radius: 999px; font-size: 10px; font-weight: 500; vertical-align: 1px; background: color-mix(in srgb, var(--color-danger) 14%, transparent); color: var(--color-danger); }
.inst-moderation h3 { margin: 0 0 8px; font-size: 14px; }
.inst-moderation .is-danger { color: var(--color-danger); }
.inst-badge { margin: 0; align-self: flex-start; padding: 3px 10px; border-radius: 999px; font-size: 12px; background: var(--color-fill); color: var(--color-label-secondary); }
.inst-badge.is-federating, .inst-badge.is-subscribing, .inst-badge.is-publishing { background: color-mix(in srgb, var(--admin-accent) 14%, transparent); color: var(--admin-accent); }
.inst-badge.is-failing { background: color-mix(in srgb, var(--color-danger) 14%, transparent); color: var(--color-danger); }
.inst-chart { margin: 0; }
.inst-chart figcaption { font-size: 12px; color: var(--color-label-secondary); margin-bottom: 6px; }
.inst-stats { display: grid; grid-template-columns: repeat(2, 1fr); gap: 10px; margin: 0; }
.inst-stats dt, .inst-meta dt { font-size: 12px; color: var(--color-label-secondary); }
.inst-stats dd { margin: 0; font-size: 22px; font-weight: 600; font-variant-numeric: tabular-nums; }
.inst-meta { margin: 0; display: flex; flex-direction: column; gap: 6px; font-size: 13px; }
.inst-meta div { display: grid; grid-template-columns: 96px 1fr; gap: 8px; }
.inst-meta dd { margin: 0; }
.inst-meta .bad { color: var(--color-danger); }
.inst-actions { display: flex; gap: 8px; flex-wrap: wrap; }
.inst-actions a { text-decoration: none; }
.sr-only { position: absolute; width: 1px; height: 1px; overflow: hidden; clip: rect(0 0 0 0); }
@media (max-width: 1100px) { .inst-grid { grid-template-columns: repeat(2, minmax(0, 1fr)); } }
@media (max-width: 640px) { .inst-grid { grid-template-columns: 1fr; } }
</style>
