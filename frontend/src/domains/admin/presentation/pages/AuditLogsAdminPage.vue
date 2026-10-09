<script setup lang="ts">
import { onMounted, ref, reactive } from 'vue'
import { announcementsAuditApi as api, type AuditLog } from '../../infrastructure/announcementsAuditApi'
const filters = reactive({ actor_id: '', action: '', outcome: '', from: '', to: '' })
const rows = ref<AuditLog[]>([])
const cursors = ref([''])
const loading = ref(false)
const error = ref('')
const names = { pending: '结果待确认', success: '成功', failure: '失败' }
async function load(reset = false) {
  if (loading.value) return
  if (reset) cursors.value = ['']
  loading.value = true; error.value = ''
  try { rows.value = await api.logs({ ...filters, from: filters.from ? new Date(filters.from).toISOString() : '', to: filters.to ? new Date(filters.to).toISOString() : '', max_id: cursors.value.at(-1) || '' }) }
  catch (e) { rows.value = []; error.value = e instanceof Error ? e.message : '读取失败' }
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
    <header><h1>审计日志</h1><p>管理操作的身份与结果；“待确认”不代表成功。</p></header>
    <form class="admin-toolbar" @submit.prevent="load(true)">
      <label class="admin-field">管理员用户 ID<input v-model="filters.actor_id" inputmode="numeric" pattern="[0-9]*" /></label>
      <label class="admin-field">操作（精确 method + route）<input v-model="filters.action" placeholder="POST /api/v1/admin/accounts/:id/action" /></label>
      <label class="admin-field">结果<select v-model="filters.outcome"><option value="">全部</option><option value="success">成功</option><option value="failure">失败</option><option value="pending">待确认</option></select></label>
      <label class="admin-field">开始时间<input v-model="filters.from" type="datetime-local" /></label>
      <label class="admin-field">结束时间<input v-model="filters.to" type="datetime-local" /></label>
      <button class="admin-button admin-button--primary" :disabled="loading">筛选</button>
    </form>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="admin-empty" role="status">正在加载…</p>
    <div v-else class="admin-card">
      <div class="admin-table-wrap" role="region" aria-label="审计日志，可横向滚动" tabindex="0">
        <table class="admin-table">
          <thead><tr><th>时间</th><th>管理员 / 账号</th><th>操作 / 目标</th><th>结果</th><th>HTTP / 耗时</th></tr></thead>
          <tbody><tr v-for="row in rows" :key="row.id"><td>{{ new Date(row.created_at).toLocaleString() }}</td><td>#{{ row.actor_id }} / #{{ row.account_id }}</td><td>{{ row.action }}<br />目标 #{{ row.target_id || '—' }}</td><td><span class="admin-badge" :class="{'admin-badge--success':row.outcome==='success','admin-badge--danger':row.outcome==='failure','admin-badge--warning':row.outcome==='pending'}">{{ names[row.outcome] }}</span></td><td>{{ row.status || '—' }} / {{ row.duration_ms }} ms</td></tr></tbody>
        </table>
      </div>
      <p v-if="!rows.length" class="admin-empty">没有匹配的审计日志</p>
    </div>
    <nav class="admin-pagination" aria-label="审计日志分页"><button class="admin-button" :disabled="loading || cursors.length === 1" @click="page(false)">上一页</button><button class="admin-button" :disabled="loading || rows.length < 40" @click="page(true)">下一页</button></nav>
  </section>
</template>
