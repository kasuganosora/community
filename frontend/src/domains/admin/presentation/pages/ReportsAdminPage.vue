<script setup lang="ts">
import { displayAccount } from '@/shared/utils/accountDisplay'
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { moderationAdminApi as api, type AdminReport, type ReportAction } from '../../infrastructure/moderationAdminApi'
import RichText from '@/shared/components/common/RichText.vue'
import { t } from '@/shared/i18n'
const filters = reactive({ resolved: 'false', account_id: '', target_account_id: '' })
const items = ref<AdminReport[]>([])
const selected = ref<AdminReport | null>(null)
const cursors = ref<string[]>([''])
const loading = ref(false)
const busy = ref(false)
const detailLoading = ref(false)
const error = ref('')
const limit = 40
let listRequest = 0
let detailRequest = 0
const labels: Record<ReportAction, string> = { assign_to_self: t('admin.ReportsAdminPage.s34'), unassign: t('admin.ReportsAdminPage.s35'), resolve: t('admin.ReportsAdminPage.s37'), reopen: t('admin.ReportsAdminPage.s36') }
const categories: Record<string, string> = { spam: t('admin.ReportsAdminPage.s45'), other: t('admin.ReportsAdminPage.s46'), legal: t('admin.ReportsAdminPage.s47'), violation: t('admin.ReportsAdminPage.s48') }
function message(e: unknown) { return e instanceof Error ? e.message : t('admin.ReportsAdminPage.s49') }
async function load(reset = false) {
  if (reset) { cursors.value = ['']; selected.value = null; detailLoading.value = false; detailRequest++ }
  const version = ++listRequest
  loading.value = true
  error.value = ''
  try {
    const result = await api.reports({ ...filters, account_id: filters.account_id.trim(), target_account_id: filters.target_account_id.trim(), max_id: cursors.value.at(-1) || undefined, limit })
    if (version === listRequest) items.value = result
  } catch (e) { if (version === listRequest) { items.value = []; error.value = message(e) } }
  finally { if (version === listRequest) loading.value = false }
}
async function detail(id: string) {
  const version = ++detailRequest
  selected.value = null
  detailLoading.value = true
  error.value = ''
  try { const report = await api.report(id); if (version === detailRequest) selected.value = report }
  catch (e) { if (version === detailRequest) error.value = message(e) }
  finally { if (version === detailRequest) detailLoading.value = false }
}
async function act(action: ReportAction) {
  const report = selected.value
  if (!report || busy.value || !window.confirm(t('admin.ReportsAdminPage.s50', [report.id, labels[action]]))) return
  busy.value = true
  error.value = ''
  try { await api.reportAction(report.id, action); await detail(report.id); await load() }
  catch (e) { error.value = message(e) }
  finally { busy.value = false }
}
async function deleteStatus(id: string) {
  const report = selected.value
  if (!report || busy.value || !window.confirm(t('admin.ReportsAdminPage.s51', [id]))) return
  busy.value = true
  error.value = ''
  try { await api.deleteStatus(id); await detail(report.id) }
  catch (e) { error.value = message(e) }
  finally { busy.value = false }
}
async function page(next: boolean) {
  if (next) {
    const last = items.value.at(-1)
    if (!last) return
    cursors.value.push(last.id)
  } else cursors.value.pop()
  await load()
}
onMounted(() => load())
onBeforeUnmount(() => { listRequest++; detailRequest++ })
</script>
<template>
  <section class="admin-page">
    <header>
      <h1>{{ $t('admin.ReportsAdminPage.s1') }}</h1>
      <p>{{ $t('admin.ReportsAdminPage.s2') }}</p>
    </header>
    <form class="admin-toolbar" :aria-label="$t('admin.ReportsAdminPage.s3')" @submit.prevent="!busy && load(true)">
      <label class="admin-field">{{ $t('admin.ReportsAdminPage.s4') }}<select v-model="filters.resolved"><option value="">{{ $t('admin.ReportsAdminPage.s5') }}</option><option value="false">{{ $t('admin.ReportsAdminPage.s6') }}</option><option value="true">{{ $t('admin.ReportsAdminPage.s7') }}</option></select></label>
      <label class="admin-field">{{ $t('admin.ReportsAdminPage.s8') }}<input v-model="filters.account_id" inputmode="numeric" pattern="[0-9]*" /></label>
      <label class="admin-field">{{ $t('admin.ReportsAdminPage.s9') }}<input v-model="filters.target_account_id" inputmode="numeric" pattern="[0-9]*" /></label>
      <button class="admin-button admin-button--primary" :disabled="loading || busy">{{ $t('admin.ReportsAdminPage.s10') }}</button>
    </form>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.ReportsAdminPage.s11') }}</p>
    <div class="admin-card admin-table-wrap" role="region" :aria-label="$t('admin.ReportsAdminPage.s12')" tabindex="0">
      <table class="admin-table">
        <thead><tr><th>{{ $t('admin.ReportsAdminPage.s13') }}</th><th>{{ $t('admin.ReportsAdminPage.s14') }}</th><th>{{ $t('admin.ReportsAdminPage.s15') }}</th><th>{{ $t('admin.ReportsAdminPage.s16') }}</th><th>{{ $t('admin.ReportsAdminPage.s17') }}</th><th>{{ $t('admin.ReportsAdminPage.s18') }}</th></tr></thead>
        <tbody><tr v-for="report in items" :key="report.id"><td>#{{ report.id }}</td><td>{{ displayAccount(report.target_account?.acct) || $t('admin.ReportsAdminPage.s19') }}</td><td>{{ categories[report.category] || report.category }}</td><td><span class="admin-badge">{{ report.action_taken ? $t('admin.ReportsAdminPage.s7') : $t('admin.ReportsAdminPage.s6') }}</span></td><td>{{ displayAccount(report.assigned_account?.acct) || $t('admin.ReportsAdminPage.s20') }}</td><td><button class="admin-button" :disabled="loading || busy" @click="detail(report.id)">{{ $t('admin.ReportsAdminPage.s21') }}</button></td></tr></tbody>
      </table>
      <p v-if="!loading && !error && !items.length" class="admin-empty">{{ $t('admin.ReportsAdminPage.s22') }}</p>
    </div>
    <nav class="admin-pagination" :aria-label="$t('admin.ReportsAdminPage.s23')"><button class="admin-button" :disabled="loading || busy || cursors.length === 1" @click="page(false)">{{ $t('admin.ReportsAdminPage.s24') }}</button><span>{{ $t('admin.ReportsAdminPage.s25', [cursors.length]) }}</span><button class="admin-button" :disabled="loading || busy || items.length < limit" @click="page(true)">{{ $t('admin.ReportsAdminPage.s26') }}</button></nav>
    <p v-if="detailLoading" class="admin-empty" role="status">{{ $t('admin.ReportsAdminPage.s27') }}</p>
    <section v-if="selected" class="admin-card" :aria-label="$t('admin.ReportsAdminPage.s28')">
      <h2>{{ $t('admin.ReportsAdminPage.s29', [selected.id, selected.action_taken ? $t('admin.ReportsAdminPage.s7') : $t('admin.ReportsAdminPage.s6')]) }}</h2>
      <dl><dt>{{ $t('admin.ReportsAdminPage.s30') }}</dt><dd>{{ displayAccount(selected.account?.acct) || $t('admin.ReportsAdminPage.s19') }}</dd><dt>{{ $t('admin.ReportsAdminPage.s14') }}</dt><dd>{{ displayAccount(selected.target_account?.acct) || $t('admin.ReportsAdminPage.s19') }}</dd><dt>{{ $t('admin.ReportsAdminPage.s17') }}</dt><dd>{{ displayAccount(selected.assigned_account?.acct) || $t('admin.ReportsAdminPage.s20') }}</dd></dl>
      <h3>{{ $t('admin.ReportsAdminPage.s31') }}</h3><p class="report-comment">{{ selected.comment || $t('admin.ReportsAdminPage.s32') }}</p>
      <div class="admin-toolbar">
        <button class="admin-button" :disabled="busy" @click="act('assign_to_self')">{{ selected.assigned_account ? $t('admin.ReportsAdminPage.s33') : $t('admin.ReportsAdminPage.s34') }}</button>
        <button v-if="selected.assigned_account" class="admin-button" :disabled="busy" @click="act('unassign')">{{ $t('admin.ReportsAdminPage.s35') }}</button>
        <button v-if="selected.action_taken" class="admin-button" :disabled="busy" @click="act('reopen')">{{ $t('admin.ReportsAdminPage.s36') }}</button>
        <button v-else class="admin-button admin-button--primary" :disabled="busy" @click="act('resolve')">{{ $t('admin.ReportsAdminPage.s37') }}</button>
      </div>
      <h3>{{ $t('admin.ReportsAdminPage.s38') }}</h3>
      <p v-if="!selected.status_ids?.length" class="admin-empty">{{ $t('admin.ReportsAdminPage.s39') }}</p>
      <p v-else-if="!selected.statuses?.length" class="admin-empty">{{ $t('admin.ReportsAdminPage.s40') }}</p>
      <p v-else-if="selected.status_ids.length > (selected.statuses?.length || 0)" class="admin-empty">{{ $t('admin.ReportsAdminPage.s41', [selected.status_ids.length - (selected.statuses?.length || 0)]) }}</p>
      <article v-for="status in selected.statuses || []" :key="status.id" class="reported-status">
        <p><strong>{{ displayAccount(status.account?.acct) || $t('admin.ReportsAdminPage.s19') }}</strong> · #{{ status.id }} · {{ status.visibility }}</p>
        <p v-if="status.spoiler_text">{{ $t('admin.ReportsAdminPage.s42', [status.spoiler_text]) }}</p>
        <button class="admin-button admin-button--danger" :disabled="busy" @click="deleteStatus(status.id)">{{ $t('admin.ReportsAdminPage.s43') }}</button>
        <RichText :html="status.content" :emojis="status.emojis" />
        <p v-if="status.media_attachments?.length">{{ $t('admin.ReportsAdminPage.s44', [status.media_attachments.length]) }}</p>
      </article>
    </section>
  </section>
</template>
<style scoped>
.report-comment { white-space: pre-wrap; overflow-wrap: anywhere; }
.reported-status { border-top: 1px solid var(--color-separator); margin-top: 1rem; padding-top: 1rem; overflow-wrap: anywhere; }
</style>
