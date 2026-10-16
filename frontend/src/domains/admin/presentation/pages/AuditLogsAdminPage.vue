<script setup lang="ts">
import { onMounted, ref, reactive } from 'vue'
import { announcementsAuditApi as api, type AuditLog } from '../../infrastructure/announcementsAuditApi'
import { t } from '@/shared/i18n'
const filters = reactive({ actor_id: '', action: '', outcome: '', from: '', to: '' })
const rows = ref<AuditLog[]>([])
const cursors = ref([''])
const loading = ref(false)
const error = ref('')
const names = { pending: t('admin.AuditLogsAdminPage.s24'), success: t('admin.AuditLogsAdminPage.s7'), failure: t('admin.AuditLogsAdminPage.s8') }
async function load(reset = false) {
  if (loading.value) return
  if (reset) cursors.value = ['']
  loading.value = true; error.value = ''
  try { rows.value = await api.logs({ ...filters, from: filters.from ? new Date(filters.from).toISOString() : '', to: filters.to ? new Date(filters.to).toISOString() : '', max_id: cursors.value.at(-1) || '' }) }
  catch (e) { rows.value = []; error.value = e instanceof Error ? e.message : t('admin.AuditLogsAdminPage.s25') }
  finally { loading.value = false }
}
function page(next: boolean) {
  if (next) cursors.value.push(rows.value.at(-1)?.id || '')
  else cursors.value.pop()
  void load()
}
onMounted(() => load())
</script>
<template>
  <section class="admin-page">
    <header><h1>{{ $t('admin.AuditLogsAdminPage.s1') }}</h1><p>{{ $t('admin.AuditLogsAdminPage.s2') }}</p></header>
    <form class="admin-toolbar" @submit.prevent="load(true)">
      <label class="admin-field">{{ $t('admin.AuditLogsAdminPage.s3') }}<input v-model="filters.actor_id" inputmode="numeric" pattern="[0-9]*" /></label>
      <label class="admin-field">{{ $t('admin.AuditLogsAdminPage.s4') }}<input v-model="filters.action" placeholder="POST /api/v1/admin/accounts/:id/action" /></label>
      <label class="admin-field">{{ $t('admin.AuditLogsAdminPage.s5') }}<select v-model="filters.outcome"><option value="">{{ $t('admin.AuditLogsAdminPage.s6') }}</option><option value="success">{{ $t('admin.AuditLogsAdminPage.s7') }}</option><option value="failure">{{ $t('admin.AuditLogsAdminPage.s8') }}</option><option value="pending">{{ $t('admin.AuditLogsAdminPage.s9') }}</option></select></label>
      <label class="admin-field">{{ $t('admin.AuditLogsAdminPage.s10') }}<input v-model="filters.from" type="datetime-local" /></label>
      <label class="admin-field">{{ $t('admin.AuditLogsAdminPage.s11') }}<input v-model="filters.to" type="datetime-local" /></label>
      <button class="admin-button admin-button--primary" :disabled="loading">{{ $t('admin.AuditLogsAdminPage.s12') }}</button>
    </form>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="admin-empty" role="status">{{ $t('admin.AuditLogsAdminPage.s13') }}</p>
    <div v-else class="admin-card">
      <div class="admin-table-wrap" role="region" :aria-label="$t('admin.AuditLogsAdminPage.s14')" tabindex="0">
        <table class="admin-table">
          <thead><tr><th>{{ $t('admin.AuditLogsAdminPage.s15') }}</th><th>{{ $t('admin.AuditLogsAdminPage.s16') }}</th><th>{{ $t('admin.AuditLogsAdminPage.s17') }}</th><th>{{ $t('admin.AuditLogsAdminPage.s5') }}</th><th>{{ $t('admin.AuditLogsAdminPage.s18') }}</th></tr></thead>
          <tbody><tr v-for="row in rows" :key="row.id"><td>{{ new Date(row.created_at).toLocaleString() }}</td><td>#{{ row.actor_id }} / #{{ row.account_id }}</td><td>{{ row.action }}<br />{{ $t('admin.AuditLogsAdminPage.s19', [row.target_id || '—']) }}</td><td><span class="admin-badge" :class="{'admin-badge--success':row.outcome==='success','admin-badge--danger':row.outcome==='failure','admin-badge--warning':row.outcome==='pending'}">{{ names[row.outcome] }}</span></td><td>{{ row.status || '—' }} / {{ row.duration_ms }} ms</td></tr></tbody>
        </table>
      </div>
      <p v-if="!error && !rows.length" class="admin-empty">{{ $t('admin.AuditLogsAdminPage.s20') }}</p>
    </div>
    <nav class="admin-pagination" :aria-label="$t('admin.AuditLogsAdminPage.s21')"><button class="admin-button" :disabled="loading || cursors.length === 1" @click="page(false)">{{ $t('admin.AuditLogsAdminPage.s22') }}</button><button class="admin-button" :disabled="loading || rows.length < 40" @click="page(true)">{{ $t('admin.AuditLogsAdminPage.s23') }}</button></nav>
  </section>
</template>
