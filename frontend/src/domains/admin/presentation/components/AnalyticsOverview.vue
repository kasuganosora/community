<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import { analyticsAdminApi, type AdminAnalytics, type AnalyticsDay } from '../../infrastructure/analyticsAdminApi'
import { t } from '@/shared/i18n'
const data = ref<AdminAnalytics | null>(null), error = ref(''), loading = ref(false)
const days = ref<7 | 30>(30)
const metric = ref<'local_posts' | 'posting_accounts'>('local_posts')
const selected = ref<AnalyticsDay | null>(null)
const visible = computed(() => data.value?.daily.slice(-days.value) ?? [])
const maximum = computed(() => Math.max(1, ...visible.value.map(day => day[metric.value])))
const heatMax = computed(() => Math.max(1, ...(data.value?.daily.map(day => day.local_posts) ?? [])))
const labels: Record<string, string> = { local_accounts: t('admin.AnalyticsOverview.s45'), remote_accounts: t('admin.AnalyticsOverview.s46'), local_posts: t('admin.AnalyticsOverview.s22'), remote_posts: t('admin.AnalyticsOverview.s23'), instances: t('admin.AnalyticsOverview.s47'), local_emojis: t('admin.AnalyticsOverview.s48'), remote_emojis: t('admin.AnalyticsOverview.s49') }
const points = computed(() => visible.value.map((day, i) => `${20 + i * 660 / Math.max(1, visible.value.length - 1)},${160 - day[metric.value] * 140 / maximum.value}`).join(' '))
async function load() {
  if (loading.value) return
  loading.value = true; error.value = ''; data.value = null; selected.value = null
  try { data.value = await analyticsAdminApi.get(days.value) }
  catch (cause) { error.value = cause instanceof Error ? cause.message : t('admin.AnalyticsOverview.s50') }
  finally { loading.value = false }
}
function percent(size: number, retained: number) { return size ? `${(100 * retained / size).toFixed(1)}%` : '—' }
onMounted(load)
</script>
<template>
  <section class="analytics" :aria-label="$t('admin.AnalyticsOverview.s1')" :aria-busy="loading">
    <header><div><h2>{{ $t('admin.AnalyticsOverview.s1') }}</h2><p>{{ $t('admin.AnalyticsOverview.s2') }}</p></div><button class="admin-button" :disabled="loading" @click="load">{{ $t('admin.AnalyticsOverview.s3') }}</button></header>
    <p v-if="loading" role="status">{{ $t('admin.AnalyticsOverview.s4') }}</p>
    <p v-else-if="error" role="alert" class="admin-error">{{ error }} <button class="admin-button" @click="load">{{ $t('admin.AnalyticsOverview.s5') }}</button></p>
    <template v-else-if="data">
      <dl class="analytics-totals"><div v-for="(label, key) in labels" :key="key"><dt>{{ label }}</dt><dd>{{ data.totals[key]?.toLocaleString() ?? '—' }}</dd></div></dl>
      <p class="analytics-note">{{ $t('admin.AnalyticsOverview.s6') }}</p>
      <section class="admin-card analytics-trend">
        <header><h3>{{ $t('admin.AnalyticsOverview.s7') }}</h3><div class="analytics-controls"><label>{{ $t('admin.AnalyticsOverview.s8') }} <select v-model="days"><option :value="7">{{ $t('admin.AnalyticsOverview.s9') }}</option><option :value="30">{{ $t('admin.AnalyticsOverview.s10') }}</option></select></label><label>{{ $t('admin.AnalyticsOverview.s11') }} <select v-model="metric"><option value="local_posts">{{ $t('admin.AnalyticsOverview.s12') }}</option><option value="posting_accounts">{{ $t('admin.AnalyticsOverview.s13') }}</option></select></label></div></header>
        <svg viewBox="0 0 700 190" role="img" :aria-label="$t('admin.AnalyticsOverview.s15', [days, metric === 'local_posts' ? $t('admin.AnalyticsOverview.s12') : $t('admin.AnalyticsOverview.s14')])">
          <title>{{ $t('admin.AnalyticsOverview.s16') }}</title><path d="M20 20V160H680" class="axis" /><polyline :points="points" class="trend" />
          <circle v-for="(day, i) in visible" :key="day.date" :cx="20 + i * 660 / Math.max(1, visible.length - 1)" :cy="160 - day[metric] * 140 / maximum" r="4" tabindex="0" @focus="selected = day" @mouseenter="selected = day"><title>{{ day.date }}：{{ day[metric] }}</title></circle>
          <text x="20" y="185">{{ visible[0]?.date }}</text><text x="680" y="185" text-anchor="end">{{ visible.at(-1)?.date }}</text>
        </svg>
        <p aria-live="polite">{{ selected ? $t('admin.AnalyticsOverview.s17', [selected.date, selected.local_posts, selected.remote_posts, selected.posting_accounts]) : $t('admin.AnalyticsOverview.s18') }}</p>
        <details><summary>{{ $t('admin.AnalyticsOverview.s19') }}</summary><div class="analytics-table"><table><caption>{{ $t('admin.AnalyticsOverview.s20', [days]) }}</caption><thead><tr><th>{{ $t('admin.AnalyticsOverview.s21') }}</th><th>{{ $t('admin.AnalyticsOverview.s22') }}</th><th>{{ $t('admin.AnalyticsOverview.s23') }}</th><th>{{ $t('admin.AnalyticsOverview.s24') }}</th></tr></thead><tbody><tr v-for="day in visible" :key="day.date"><th>{{ day.date }}</th><td>{{ day.local_posts }}</td><td>{{ day.remote_posts }}</td><td>{{ day.posting_accounts }}</td></tr></tbody></table></div></details>
      </section>
      <section class="admin-card analytics-heat"><h3>{{ $t('admin.AnalyticsOverview.s25') }}</h3><p>{{ $t('admin.AnalyticsOverview.s26') }}</p><svg viewBox="0 0 540 148" role="img" :aria-label="$t('admin.AnalyticsOverview.s27')"><title>{{ $t('admin.AnalyticsOverview.s28') }}</title><rect v-for="(day, i) in data.daily" :key="day.date" :x="Math.floor(i / 7) * 20" :y="i % 7 * 20" width="16" height="16" rx="3" :opacity="day.local_posts ? 0.25 + 0.75 * day.local_posts / heatMax : 0.09"><title>{{ $t('admin.AnalyticsOverview.s29', [day.date, day.local_posts]) }}</title></rect></svg><details><summary>{{ $t('admin.AnalyticsOverview.s30') }}</summary><div class="analytics-table"><table><thead><tr><th>{{ $t('admin.AnalyticsOverview.s21') }}</th><th>{{ $t('admin.AnalyticsOverview.s22') }}</th></tr></thead><tbody><tr v-for="day in data.daily" :key="day.date"><th>{{ day.date }}</th><td>{{ day.local_posts }}</td></tr></tbody></table></div></details></section>
      <section class="admin-card analytics-retention"><h3>{{ $t('admin.AnalyticsOverview.s31') }}</h3><p>{{ $t('admin.AnalyticsOverview.s32') }}</p><div class="analytics-table"><table><thead><tr><th>{{ $t('admin.AnalyticsOverview.s33') }}</th><th>{{ $t('admin.AnalyticsOverview.s34') }}</th><th>{{ $t('admin.AnalyticsOverview.s35') }}</th><th>{{ $t('admin.AnalyticsOverview.s36') }}</th></tr></thead><tbody><tr v-for="cohort in data.cohorts" :key="cohort.date"><th>{{ cohort.date }}</th><td>{{ cohort.size }}</td><td>{{ cohort.retained }}</td><td>{{ percent(cohort.size, cohort.retained) }}</td></tr></tbody></table></div></section>
      <section class="admin-card analytics-federation"><h3>{{ $t('admin.AnalyticsOverview.s37') }}</h3><p>{{ $t('admin.AnalyticsOverview.s38') }}</p><div class="analytics-distributions"><div v-for="direction in (['outgoing', 'incoming'] as const)" :key="direction"><h4>{{ direction === 'outgoing' ? $t('admin.AnalyticsOverview.s39') : $t('admin.AnalyticsOverview.s40') }}</h4><p v-if="!data.federation[direction].length">{{ $t('admin.AnalyticsOverview.s41') }}</p><table v-else><thead><tr><th>{{ $t('admin.AnalyticsOverview.s42') }}</th><th>{{ $t('admin.AnalyticsOverview.s43') }}</th></tr></thead><tbody><tr v-for="row in data.federation[direction]" :key="row.domain"><th>{{ row.domain }}</th><td>{{ row.count }}</td></tr></tbody></table></div></div></section>
      <p class="analytics-note">{{ $t('admin.AnalyticsOverview.s44', [new Date(data.generated_at).toLocaleString()]) }}</p>
    </template>
  </section>
</template>
<style scoped>
.analytics{display:grid;grid-template-columns:repeat(2,minmax(0,1fr));gap:20px}.analytics>*{min-width:0;grid-column:1/-1}.analytics-heat,.analytics-retention{grid-column:auto}.analytics-heat{align-self:start}.analytics header{display:flex;justify-content:space-between;align-items:center;gap:16px;flex-wrap:wrap}.analytics h2,.analytics h3{margin:0}.analytics p{font-size:13px;color:var(--color-label-secondary);line-height:1.6}.analytics-totals{display:grid;grid-template-columns:repeat(auto-fit,minmax(120px,1fr));gap:12px;margin:0}.analytics-totals>div{padding:16px;border:1px solid var(--color-separator);border-radius:12px}.analytics dt{font-size:12px}.analytics dd{font-size:24px;margin:8px 0 0;font-variant-numeric:tabular-nums}.analytics-controls{display:flex;gap:12px;flex-wrap:wrap}.analytics label{font-size:13px;display:grid;gap:6px;min-width:0}.analytics-controls{align-items:end}.analytics-totals>div{background:var(--color-bg-row)}.analytics select{padding:6px;color:inherit;background:var(--color-bg-grouped);border:1px solid var(--color-separator);border-radius:6px}.analytics svg{width:100%;max-height:270px;overflow:visible}.analytics svg rect,.analytics svg circle{fill:var(--admin-accent,#397dda)}.analytics svg circle:focus{outline:2px solid currentColor}.analytics svg text{font-size:11px;fill:currentColor}.axis{fill:none;stroke:var(--color-separator)}.trend{fill:none;stroke:var(--admin-accent,#397dda);stroke-width:2}.analytics-table{overflow:auto;max-height:360px}.analytics table{width:100%;border-collapse:collapse;font-size:13px}.analytics th,.analytics td{text-align:left;padding:9px;border-bottom:1px solid var(--color-separator);overflow-wrap:anywhere}.analytics td{font-variant-numeric:tabular-nums}.analytics summary{cursor:pointer;padding:10px 0}.analytics-distributions{display:grid;grid-template-columns:1fr 1fr;gap:24px}@media(max-width:1100px){.analytics{grid-template-columns:1fr}}@media(max-width:700px){.analytics-distributions{grid-template-columns:1fr}.analytics-totals{grid-template-columns:repeat(2,minmax(0,1fr))}}
</style>
