<script setup lang="ts">
import { ref, computed, onMounted, onBeforeUnmount, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { discoveryAdminApi as api, type DiscoveryAdminStatus, type DiscoverySuppression } from '../../infrastructure/discoveryAdminApi'
import { invalidateDiscovery } from '@/domains/discovery/presentation/composables/discoveryState'
import type { TargetKind } from '@/domains/discovery/infrastructure/discoveryApi'
import { t } from '@/shared/i18n'
const status = ref<DiscoveryAdminStatus | null>(null), rows = ref<DiscoverySuppression[]>([])
const loading = ref(false), busy = ref(false), error = ref(''), success = ref('')
const approving = ref(false)
const kind = ref<TargetKind>('account'), target = ref(''), reason = ref('')
const kindLabel = { account: t('admin.DiscoveryAdminPage.s48'), status: t('admin.DiscoveryAdminPage.s49'), tag: t('admin.DiscoveryAdminPage.s50') }
const modeLabel: Record<string, string> = { rules: t('admin.DiscoveryAdminPage.s61'), meili_lexical: t('admin.DiscoveryAdminPage.s62'), meili_hybrid: t('admin.DiscoveryAdminPage.s63'), disabled: t('admin.DiscoveryAdminPage.s8'), chronological_fallback: t('admin.DiscoveryAdminPage.s64') }
const reasonLabel: Record<string, string> = {
  not_built: t('admin.DiscoveryAdminPage.s65'), not_initialized: t('admin.DiscoveryAdminPage.s66'), meilisearch_disabled: t('admin.DiscoveryAdminPage.s67'),
  recommendations_disabled: t('admin.DiscoveryAdminPage.s68'), settings_unavailable: t('admin.DiscoveryAdminPage.s69'), rules_configured: t('admin.DiscoveryAdminPage.s70'),
  build_unavailable: t('admin.DiscoveryAdminPage.s71'), query_unavailable: t('admin.DiscoveryAdminPage.s72'), built: t('admin.DiscoveryAdminPage.s73'), ready: t('admin.DiscoveryAdminPage.s74'),
  resource_budget_exceeded: t('admin.DiscoveryAdminPage.s75'),
  redis_unavailable: t('admin.DiscoveryAdminPage.s76'), configuration_changed: t('admin.DiscoveryAdminPage.s77'), index_expired: t('admin.DiscoveryAdminPage.s78'),
  quality_not_approved: t('admin.DiscoveryAdminPage.s79'), awaiting_manual_approval: t('admin.DiscoveryAdminPage.s80'), index_empty: t('admin.DiscoveryAdminPage.s81'), approval_required: t('admin.DiscoveryAdminPage.s82'),
  pool_not_built: t('admin.DiscoveryAdminPage.s83'), invalid_pool: t('admin.DiscoveryAdminPage.s84'), pool_expired: t('admin.DiscoveryAdminPage.s85'),
}
function reasonText(code?: string) { return code ? reasonLabel[code] || t('admin.DiscoveryAdminPage.s86') : t('admin.DiscoveryAdminPage.s87') }
function bytes(value: number) { return value >= 1048576 ? `${(value / 1048576).toFixed(1)} MiB` : `${(value / 1024).toFixed(1)} KiB` }
let version = 0, active = true
function date(value?: string) { return value && !value.startsWith('0001-') ? new Date(value).toLocaleString() : t('admin.DiscoveryAdminPage.s88') }
async function load() {
  const mine = ++version, epoch = sessionEpoch.value
  loading.value = true; error.value = ''
  try {
    const [runtime, rules] = await Promise.all([api.status(), api.feedback()])
    if (mine !== version || epoch !== sessionEpoch.value) return
    status.value = runtime; rows.value = rules
  } catch (e) { if (mine === version) error.value = e instanceof Error ? e.message : t('admin.DiscoveryAdminPage.s89') }
  finally { if (mine === version) loading.value = false }
}
async function suppress() {
  if (busy.value || !target.value.trim() || !reason.value.trim()) return
  busy.value = true; error.value = ''; success.value = ''
  const epoch = sessionEpoch.value
  try {
    await api.suppress({ target_kind: kind.value, target_key: target.value.trim(), reason: reason.value.trim() })
    if (epoch !== sessionEpoch.value) return
    target.value = ''; reason.value = ''; invalidateDiscovery(); await load(); success.value = t('admin.DiscoveryAdminPage.s90')
  } catch (e) { if (epoch === sessionEpoch.value) error.value = e instanceof Error ? e.message : t('admin.DiscoveryAdminPage.s91') }
  finally { if (epoch === sessionEpoch.value) busy.value = false }
}
const approvalText = computed(() => {
  const s = status.value
  if (!s) return ''
  if (!s.require_manual_approval) return t('admin.DiscoveryAdminPage.s92')
  if (!s.approved_version) return t('admin.DiscoveryAdminPage.s93')
  return s.index.version && s.approved_version === s.index.version ? t('admin.DiscoveryAdminPage.s94') : t('admin.DiscoveryAdminPage.s95')
})
const canApprove = computed(() => status.value?.require_manual_approval === true && !!status.value.index.version && status.value.approved_version !== status.value.index.version)
async function approve() {
  const current = status.value?.index.version
  if (busy.value || loading.value || !canApprove.value || !current) return
  busy.value = true; approving.value = true; error.value = ''; success.value = ''
  const epoch = sessionEpoch.value
  const isCurrent = () => active && epoch === sessionEpoch.value
  try {
    const result = await api.approve(current)
    if (!isCurrent()) return
    // Keep the confirmed mutation even if the following status refresh fails.
    if (status.value) status.value = { ...status.value, approved_version: result.approved_version }
    invalidateDiscovery()
    await load()
    if (isCurrent()) success.value = t('admin.DiscoveryAdminPage.s96')
  } catch (e) { if (isCurrent()) error.value = e instanceof Error ? e.message : t('admin.DiscoveryAdminPage.s97') }
  finally { if (isCurrent()) { busy.value = false; approving.value = false } }
}
async function restore(row: DiscoverySuppression) {
  if (busy.value) return
  busy.value = true; error.value = ''; success.value = ''
  const epoch = sessionEpoch.value
  try {
    await api.restore(row.id)
    if (epoch !== sessionEpoch.value) return
    rows.value = rows.value.filter(item => item.id !== row.id); invalidateDiscovery(); success.value = t('admin.DiscoveryAdminPage.s98')
  } catch (e) { if (epoch === sessionEpoch.value) error.value = e instanceof Error ? e.message : t('admin.DiscoveryAdminPage.s99') }
  finally { if (epoch === sessionEpoch.value) busy.value = false }
}
watch(sessionEpoch, () => { ++version; status.value = null; rows.value = []; busy.value = false; approving.value = false; success.value = ''; void load() }, { flush: 'sync' })
onMounted(load)
onBeforeUnmount(() => { active = false; ++version })
</script>

<template>
  <div class="admin-page admin-discovery">
    <header><h1>{{ $t('admin.DiscoveryAdminPage.s1') }}</h1><p>{{ $t('admin.DiscoveryAdminPage.s2') }}</p></header>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="success" class="admin-notice" role="status">{{ success }}</p>
    <section class="admin-card" :aria-busy="loading">
      <div class="discovery-admin-heading"><h2>{{ $t('admin.DiscoveryAdminPage.s3') }}</h2><button type="button" class="admin-button" :disabled="loading || busy" @click="load">{{ loading ? $t('admin.DiscoveryAdminPage.s4') : $t('admin.DiscoveryAdminPage.s5') }}</button></div>
      <dl v-if="status" class="discovery-admin-stats">
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s6') }}</dt><dd>{{ status.enabled ? $t('admin.DiscoveryAdminPage.s7') : $t('admin.DiscoveryAdminPage.s8') }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s9') }}</dt><dd>{{ status.trends_enabled ? $t('admin.DiscoveryAdminPage.s7') : $t('admin.DiscoveryAdminPage.s8') }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s10') }}</dt><dd>{{ !status.enabled ? $t('admin.DiscoveryAdminPage.s8') : modeLabel[status.index.mode || 'rules'] || status.index.mode }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s11') }}</dt><dd>{{ status.algorithm_version }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s12') }}</dt><dd>{{ status.index.ready ? $t('admin.DiscoveryAdminPage.s13') : $t('admin.DiscoveryAdminPage.s14') }}<small>{{ reasonText(status.index.reason) }}</small></dd></div>
        <div v-if="status.index.last_build_error"><dt>{{ $t('admin.DiscoveryAdminPage.s15') }}</dt><dd>{{ reasonText(status.index.last_build_error) }}<small>{{ date(status.index.last_build_error_at) }}</small></dd></div>
        <div v-if="status.index.last_query_error"><dt>{{ $t('admin.DiscoveryAdminPage.s16') }}</dt><dd>{{ reasonText(status.index.last_query_error) }}<small>{{ date(status.index.last_query_error_at) }}</small></dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s17') }}</dt><dd>{{ status.index.document_count }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s18') }}</dt><dd>{{ date(status.index.data_cutoff) }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s19') }}</dt><dd>{{ date(status.index.checked_at) }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s20') }}</dt><dd>{{ status.index.version || $t('admin.DiscoveryAdminPage.s21') }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s22') }}</dt><dd data-testid="discovery-approval">{{ approvalText }}<small v-if="status.approved_version">{{ $t('admin.DiscoveryAdminPage.s23', [status.approved_version]) }}</small></dd></div>
      </dl>
      <p v-if="status" class="admin-hint">{{ status.require_manual_approval ? $t('admin.DiscoveryAdminPage.s24') : $t('admin.DiscoveryAdminPage.s25') }}</p>
      <button v-if="status && canApprove" type="button" class="admin-button admin-button--primary" data-testid="discovery-approve" :disabled="busy || loading" @click="approve">{{ approving ? $t('admin.DiscoveryAdminPage.s26') : $t('admin.DiscoveryAdminPage.s27') }}</button>
      <router-link class="admin-button" to="/admin/settings/performance">{{ $t('admin.DiscoveryAdminPage.s28') }}</router-link>
    </section>
    <section v-if="status?.runtime" class="admin-card">
      <h2>{{ $t('admin.DiscoveryAdminPage.s29') }}</h2>
      <p class="admin-hint">{{ $t('admin.DiscoveryAdminPage.s30', [reasonText(status.runtime.reason)]) }}</p>
      <dl class="discovery-admin-stats">
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s31') }}</dt><dd>{{ $t('admin.DiscoveryAdminPage.s34', [status.runtime.pool_candidates, status.runtime.pool_stale ? $t('admin.DiscoveryAdminPage.s32') : $t('admin.DiscoveryAdminPage.s33')]) }}<small v-if="status.runtime.pool_truncated">{{ $t('admin.DiscoveryAdminPage.s35') }}</small></dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s36') }}</dt><dd>{{ date(status.runtime.pool_as_of) }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s37') }}</dt><dd>{{ bytes(status.runtime.pool_payload_bytes) }}<small>{{ $t('admin.DiscoveryAdminPage.s38') }}</small></dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s39') }}</dt><dd>{{ status.runtime.pool_lease_active ? $t('admin.DiscoveryAdminPage.s40') : $t('admin.DiscoveryAdminPage.s41') }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s42') }}</dt><dd>{{ status.runtime.snapshot_reservations }} / {{ status.runtime.snapshot_maximum }}</dd></div>
        <div><dt>{{ $t('admin.DiscoveryAdminPage.s43') }}</dt><dd>{{ bytes(status.runtime.snapshot_reserved_bytes) }} / {{ bytes(status.runtime.cache_budget_bytes) }}<small>{{ $t('admin.DiscoveryAdminPage.s44') }}</small></dd></div>
      </dl>
    </section>
    <section class="admin-card">
      <h2>{{ $t('admin.DiscoveryAdminPage.s45') }}</h2><p class="admin-hint">{{ $t('admin.DiscoveryAdminPage.s46') }}</p>
      <form class="discovery-admin-form" @submit.prevent="suppress">
        <label class="admin-field">{{ $t('admin.DiscoveryAdminPage.s47') }}<select v-model="kind" :disabled="busy"><option value="account">{{ $t('admin.DiscoveryAdminPage.s48') }}</option><option value="status">{{ $t('admin.DiscoveryAdminPage.s49') }}</option><option value="tag">{{ $t('admin.DiscoveryAdminPage.s50') }}</option></select></label>
        <label class="admin-field">{{ kindLabel[kind] }}{{ kind === 'tag' ? $t('admin.DiscoveryAdminPage.s51') : ' ID' }}<input v-model="target" required maxlength="255" :disabled="busy" :placeholder="kind === 'tag' ? $t('admin.DiscoveryAdminPage.s52') : $t('admin.DiscoveryAdminPage.s53')" /></label>
        <label class="admin-field discovery-admin-form__reason">{{ $t('admin.DiscoveryAdminPage.s54') }}<textarea v-model="reason" rows="2" required maxlength="500" :disabled="busy" :placeholder="$t('admin.DiscoveryAdminPage.s55')" /></label>
        <button type="submit" class="admin-button admin-button--primary" :disabled="busy || !target.trim() || !reason.trim()">{{ $t('admin.DiscoveryAdminPage.s56') }}</button>
      </form>
    </section>
    <section class="admin-card" :aria-busy="loading">
      <h2>{{ $t('admin.DiscoveryAdminPage.s57') }}</h2>
      <p v-if="!rows.length && !loading && !error" class="admin-empty">{{ $t('admin.DiscoveryAdminPage.s58') }}</p>
      <ul v-else class="discovery-admin-rules"><li v-for="row in rows" :key="row.id"><div><strong>{{ kindLabel[row.target_kind] }} · {{ row.target_key }}</strong><p>{{ row.reason || $t('admin.DiscoveryAdminPage.s59') }}</p><small>{{ date(row.created_at) }}</small></div><button type="button" class="admin-button" :disabled="busy" @click="restore(row)">{{ $t('admin.DiscoveryAdminPage.s60') }}</button></li></ul>
    </section>
  </div>
</template>

<style scoped>
.discovery-admin-heading { display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 1rem; }
.discovery-admin-stats { display: grid; grid-template-columns: repeat(2, minmax(0, 1fr)); gap: 0 2rem; }
.discovery-admin-stats > div { padding: 1rem 0; border-bottom: 1px solid var(--color-separator); }
.discovery-admin-stats dt { color: var(--color-label-secondary); font-size: .8125rem; margin-bottom: .375rem; }
.discovery-admin-stats dd { margin: 0; overflow-wrap: anywhere; }
.discovery-admin-stats small { display: block; margin-top: .375rem; color: var(--color-label-secondary); }
.discovery-admin-form { display: grid; grid-template-columns: minmax(8rem, 1fr) minmax(10rem, 2fr); gap: 1rem; padding-top: 1rem; }
.discovery-admin-form__reason { grid-column: 1 / -1; }
.discovery-admin-form > button { justify-self: start; }
.discovery-admin-rules { list-style: none; padding: 0; }
.discovery-admin-rules li { display: flex; align-items: center; justify-content: space-between; gap: 1rem; padding: 1rem 0; border-bottom: 1px solid var(--color-separator); }
.discovery-admin-rules li > div { min-width: 0; overflow-wrap: anywhere; }
.discovery-admin-rules p, .discovery-admin-rules small { color: var(--color-label-secondary); margin-top: .375rem; }
.discovery-admin-rules button { flex-shrink: 0; }
@media (max-width: 699px) {
  .discovery-admin-stats { gap: 0 1rem; }
  .discovery-admin-form { grid-template-columns: minmax(0, 1fr); }
}
@media (max-width: 359px) { .discovery-admin-stats { grid-template-columns: minmax(0, 1fr); } }
</style>
