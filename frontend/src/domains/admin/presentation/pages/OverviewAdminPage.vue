<script setup lang="ts">
import { computed, onMounted, ref } from 'vue'
import AnalyticsOverview from '../components/AnalyticsOverview.vue'
import { dashboardApi, type AdminMeasure } from '../../infrastructure/dashboardApi'
import { instanceApi, type InstanceInfo } from '@/domains/instance/infrastructure/instanceApi'
import { t } from '@/shared/i18n'
const measures = ref<AdminMeasure[] | null>(null)
const instance = ref<InstanceInfo | null>(null)
const loading = ref(false), error = ref(''), instanceError = ref('')
const updated = ref('')
function value(key: string) { return measures.value?.find(item => item.key === key)?.total ?? '—' }
const unresolved = computed(() => {
  const opened = Number(value('opened_reports')), resolved = Number(value('resolved_reports'))
  return Number.isFinite(opened) && Number.isFinite(resolved) ? String(Math.max(0, opened - resolved)) : '—'
})
const cards = computed(() => [
  { title: t('admin.OverviewAdminPage.s30'), value: value('user_count'), hint: t('admin.OverviewAdminPage.s31'), icon: 'usergroup', color: 'blue' },
  { title: t('admin.OverviewAdminPage.s32'), value: value('status_count'), hint: t('admin.OverviewAdminPage.s33'), icon: 'chat', color: 'purple' },
  { title: t('admin.OverviewAdminPage.s34'), value: value('new_users'), hint: t('admin.OverviewAdminPage.s35'), icon: 'user-add', color: 'green' },
  { title: t('admin.OverviewAdminPage.s36'), value: unresolved.value, hint: t('admin.OverviewAdminPage.s37'), icon: 'flag', color: 'orange' },
])
async function load() {
  if (loading.value) return
  loading.value = true; error.value = ''; instanceError.value = ''; measures.value = null
  const [stats, info] = await Promise.allSettled([dashboardApi.measures(), instanceApi.getInstance()])
  if (stats.status === 'fulfilled') { measures.value = stats.value; updated.value = new Date().toLocaleTimeString() }
  else error.value = stats.reason instanceof Error ? stats.reason.message : t('admin.OverviewAdminPage.s38')
  if (info.status === 'fulfilled') instance.value = info.value
  else instanceError.value = t('admin.OverviewAdminPage.s39')
  loading.value = false
}
onMounted(load)
</script>

<template>
  <div class="admin-page" :aria-busy="loading">
    <header class="admin-page-header"><div><h1>{{ $t('admin.OverviewAdminPage.s1') }}</h1><p>{{ $t('admin.OverviewAdminPage.s2') }}</p></div><button type="button" class="admin-button" :disabled="loading" @click="load"><t-icon name="refresh" aria-hidden="true" />{{ loading ? $t('admin.OverviewAdminPage.s3') : $t('admin.OverviewAdminPage.s4') }}</button></header>
    <p v-if="error" role="alert" class="admin-error">{{ error }} <button type="button" class="admin-button" :disabled="loading" @click="load">{{ $t('admin.OverviewAdminPage.s5') }}</button></p>
    <section class="overview-metrics" :aria-label="$t('admin.OverviewAdminPage.s6')">
      <article v-for="card in cards" :key="card.title" class="admin-card overview-metric"><div class="overview-metric-top"><h2>{{ card.title }}</h2><span class="overview-metric-icon" :class="`overview-metric-icon--${card.color}`"><t-icon :name="card.icon" :size="20" aria-hidden="true" /></span></div><strong>{{ loading ? '—' : card.value }}</strong><p>{{ card.hint }}</p></article>
    </section>
    <div class="overview-columns">
      <section class="admin-card overview-tasks"><div class="overview-section-heading"><h2>{{ $t('admin.OverviewAdminPage.s7') }}</h2><span class="admin-badge">{{ $t('admin.OverviewAdminPage.s8') }}</span></div><router-link to="/admin/reports" class="overview-task"><span class="overview-task-icon"><t-icon name="flag" :size="23" aria-hidden="true" /></span><span><strong>{{ $t('admin.OverviewAdminPage.s9') }}</strong><small>{{ unresolved === '—' ? $t('admin.OverviewAdminPage.s10') : $t('admin.OverviewAdminPage.s11', [unresolved]) }}</small></span><t-icon name="chevron-right" aria-hidden="true" /></router-link><router-link to="/admin/accounts" class="overview-task"><span class="overview-task-icon"><t-icon name="usergroup" :size="23" aria-hidden="true" /></span><span><strong>{{ $t('admin.OverviewAdminPage.s12') }}</strong><small>{{ $t('admin.OverviewAdminPage.s13') }}</small></span><t-icon name="chevron-right" aria-hidden="true" /></router-link><router-link to="/admin/emojis" class="overview-task"><span class="overview-task-icon"><t-icon name="happy" :size="23" aria-hidden="true" /></span><span><strong>{{ $t('admin.OverviewAdminPage.s14') }}</strong><small>{{ $t('admin.OverviewAdminPage.s15') }}</small></span><t-icon name="chevron-right" aria-hidden="true" /></router-link></section>
      <section class="admin-card overview-instance"><div class="overview-section-heading"><h2>{{ $t('admin.OverviewAdminPage.s16') }}</h2><t-icon name="internet" :size="20" aria-hidden="true" /></div><p v-if="instanceError" role="alert" class="admin-error">{{ instanceError }}</p><template v-if="instance"><strong class="overview-instance-title">{{ instance.title || 'Community' }}</strong><p class="overview-domain">{{ instance.domain || instance.uri || '—' }}</p><dl><div><dt>{{ $t('admin.OverviewAdminPage.s17') }}</dt><dd>{{ instance.registrations?.enabled === true ? $t('admin.OverviewAdminPage.s18') : instance.registrations?.enabled === false ? $t('admin.OverviewAdminPage.s19') : $t('admin.OverviewAdminPage.s20') }}</dd></div><div><dt>{{ $t('admin.OverviewAdminPage.s21') }}</dt><dd>ActivityPub · Matrix</dd></div><div><dt>{{ $t('admin.OverviewAdminPage.s22') }}</dt><dd>{{ updated || $t('admin.OverviewAdminPage.s23') }}</dd></div></dl></template><router-link to="/admin/settings" class="admin-button">{{ $t('admin.OverviewAdminPage.s24') }} <t-icon name="arrow-right" aria-hidden="true" /></router-link></section>
    </div>
    <section class="admin-card overview-federation"><div class="overview-federation-symbol" aria-hidden="true"><t-icon name="internet" :size="34" /></div><div><h2>{{ $t('admin.OverviewAdminPage.s25') }}</h2><p>{{ $t('admin.OverviewAdminPage.s26') }}</p></div><div class="overview-federation-links"><router-link to="/admin/federation" class="admin-button">{{ $t('admin.OverviewAdminPage.s27') }}</router-link><router-link to="/admin/system" class="admin-button">{{ $t('admin.OverviewAdminPage.s28') }}</router-link></div></section>
    <AnalyticsOverview />
    <p class="overview-footnote">{{ $t('admin.OverviewAdminPage.s29') }}</p>
  </div>
</template>
<style scoped>
.overview-eyebrow{font-size:10px;font-weight:600;letter-spacing:2px;color:var(--color-label-secondary);margin-bottom:10px}.overview-metrics{display:grid;grid-template-columns:repeat(4,minmax(0,1fr));gap:16px}.overview-metric{padding:20px}.overview-metric-top{display:flex;align-items:center;justify-content:space-between;gap:8px;margin-bottom:0}.overview-metric-top h2{margin:0;font-size:13px;font-weight:500;color:var(--color-label-secondary)}.overview-metric-icon{display:grid;place-items:center;width:34px;height:34px;border-radius:10px;color:#2584df;background:#2584df14}.overview-metric-icon--purple{color:#8964dc;background:#8964dc14}.overview-metric-icon--green{color:#219a80;background:#219a8014}.overview-metric-icon--orange{color:#cc861f;background:#cc861f14}.overview-metric>strong{display:block;font-size:32px;line-height:1.2;font-weight:650;letter-spacing:-1px;font-variant-numeric:tabular-nums;overflow-wrap:anywhere}.overview-metric>p{font-size:11px;color:var(--color-label-secondary);margin:0}.overview-columns{display:grid;grid-template-columns:minmax(0,1.4fr) minmax(0,1fr);gap:20px}.overview-section-heading{display:flex;justify-content:space-between;align-items:center;gap:12px;margin-bottom:0}.overview-section-heading h2{margin:0}.overview-task{display:flex;align-items:center;gap:14px;padding:12px 0;border-bottom:1px solid var(--color-separator)}.overview-task:last-child{border-bottom:0;padding-bottom:0}.overview-task>span:nth-child(2){flex:1;min-width:0}.overview-task strong{display:block;font-size:14px;font-weight:550}.overview-task small{display:block;font-size:12px;margin-top:4px;color:var(--color-label-secondary)}.overview-task-icon{background:var(--color-bg-grouped);color:var(--admin-accent);border-radius:12px;width:44px;height:44px;display:grid;place-items:center;flex-shrink:0}@media(hover:hover) and (pointer:fine){.overview-task:hover strong{color:var(--admin-accent)}}.overview-instance-title{display:block;font-size:23px;margin:0}.overview-domain{font-size:13px;color:var(--color-label-secondary);overflow-wrap:anywhere}.overview-instance dl{display:block;font-size:13px;margin:0}.overview-instance dl div{display:flex;justify-content:space-between;gap:12px;padding:7px 0}.overview-instance dt{color:var(--color-label-secondary)}.overview-instance dd{margin:0;text-align:right}.overview-instance>.admin-button{width:100%;font-size:13px}.overview-federation{display:flex;align-items:center;gap:20px}.overview-federation-symbol{color:var(--admin-accent);display:grid;place-items:center;width:64px;height:64px;flex-shrink:0;background:color-mix(in srgb,var(--admin-accent) 8%,transparent);border-radius:18px}.overview-federation h2{margin-bottom:8px;font-size:16px}.overview-federation p{font-size:12px;color:var(--color-label-secondary);margin:0}.overview-federation-links{margin-left:auto;display:flex;gap:8px;flex-shrink:0;flex-wrap:wrap}.overview-federation-links a{font-size:12px}.overview-footnote{font-size:11px;color:var(--color-label-secondary);text-align:right}
@media(max-width:1150px){.overview-metrics{grid-template-columns:repeat(2,minmax(0,1fr))}.overview-federation{flex-wrap:wrap}.overview-federation-links{margin-left:84px}}@media(max-width:900px){.overview-columns{grid-template-columns:1fr}}@media(max-width:760px){.overview-metrics{gap:12px}.overview-metric{padding:16px}.overview-metric>strong{font-size:28px}.overview-metric-top{gap:4px}.overview-metric-icon{width:28px;height:28px}.overview-federation-symbol{display:none}.overview-federation-links{margin-left:0}.overview-federation{gap:16px}.overview-columns{gap:18px}}@media(max-width:360px){.overview-metric-icon{display:none}}
</style>
