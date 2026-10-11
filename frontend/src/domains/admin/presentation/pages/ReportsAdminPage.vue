<script setup lang="ts">
import { displayAccount } from '@/shared/utils/accountDisplay'
import { onMounted, onBeforeUnmount, reactive, ref } from 'vue'
import { moderationAdminApi as api, type AdminReport, type ReportAction } from '../../infrastructure/moderationAdminApi'
import RichText from '@/shared/components/common/RichText.vue'
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
const labels: Record<ReportAction, string> = { assign_to_self: '认领举报', unassign: '取消认领', resolve: '标记已处理', reopen: '重新打开' }
const categories: Record<string, string> = { spam: '垃圾信息', other: '其他', legal: '违法内容', violation: '违反规则' }
function message(e: unknown) { return e instanceof Error ? e.message : '请求失败，请重试' }
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
  if (!report || busy.value || !window.confirm(`确认对举报 #${report.id} 执行“${labels[action]}”？此操作不会自动删除帖子或封禁账号。`)) return
  busy.value = true
  error.value = ''
  try { await api.reportAction(report.id, action); await detail(report.id); await load() }
  catch (e) { error.value = message(e) }
  finally { busy.value = false }
}
async function deleteStatus(id: string) {
  const report = selected.value
  if (!report || busy.value || !window.confirm(`确认永久删除帖子 #${id}？此操作不可撤销，举报记录会保留且不会自动标记已处理。`)) return
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
      <h1>举报管理</h1>
      <p>认领并核查举报；标记已处理不会自动删除内容或封禁账号。</p>
    </header>
    <form class="admin-toolbar" aria-label="搜索举报" @submit.prevent="!busy && load(true)">
      <label class="admin-field">处理状态<select v-model="filters.resolved"><option value="">全部</option><option value="false">未处理</option><option value="true">已处理</option></select></label>
      <label class="admin-field">举报人账号 ID<input v-model="filters.account_id" inputmode="numeric" pattern="[0-9]*" /></label>
      <label class="admin-field">被举报账号 ID<input v-model="filters.target_account_id" inputmode="numeric" pattern="[0-9]*" /></label>
      <button class="admin-button admin-button--primary" :disabled="loading || busy">搜索</button>
    </form>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <p v-if="loading" class="admin-empty" role="status">正在加载举报…</p>
    <div class="admin-card admin-table-wrap" role="region" aria-label="举报列表，可横向滚动" tabindex="0">
      <table class="admin-table">
        <thead><tr><th>举报</th><th>被举报账号</th><th>类别</th><th>状态</th><th>负责人</th><th>操作</th></tr></thead>
        <tbody><tr v-for="report in items" :key="report.id"><td>#{{ report.id }}</td><td>{{ displayAccount(report.target_account?.acct) || '账号已不可用' }}</td><td>{{ categories[report.category] || report.category }}</td><td><span class="admin-badge">{{ report.action_taken ? '已处理' : '未处理' }}</span></td><td>{{ displayAccount(report.assigned_account?.acct) || '未认领' }}</td><td><button class="admin-button" :disabled="loading || busy" @click="detail(report.id)">详情</button></td></tr></tbody>
      </table>
      <p v-if="!loading && !items.length" class="admin-empty">没有符合条件的举报</p>
    </div>
    <nav class="admin-pagination" aria-label="举报分页"><button class="admin-button" :disabled="loading || busy || cursors.length === 1" @click="page(false)">上一页</button><span>第 {{ cursors.length }} 页</span><button class="admin-button" :disabled="loading || busy || items.length < limit" @click="page(true)">下一页</button></nav>
    <p v-if="detailLoading" class="admin-empty" role="status">正在加载详情…</p>
    <section v-if="selected" class="admin-card" aria-label="举报详情">
      <h2>举报 #{{ selected.id }} · {{ selected.action_taken ? '已处理' : '未处理' }}</h2>
      <dl><dt>举报人</dt><dd>{{ displayAccount(selected.account?.acct) || '账号已不可用' }}</dd><dt>被举报账号</dt><dd>{{ displayAccount(selected.target_account?.acct) || '账号已不可用' }}</dd><dt>负责人</dt><dd>{{ displayAccount(selected.assigned_account?.acct) || '未认领' }}</dd></dl>
      <h3>举报说明</h3><p class="report-comment">{{ selected.comment || '未提供文字说明' }}</p>
      <div class="admin-toolbar">
        <button class="admin-button" :disabled="busy" @click="act('assign_to_self')">{{ selected.assigned_account ? '转由我处理' : '认领举报' }}</button>
        <button v-if="selected.assigned_account" class="admin-button" :disabled="busy" @click="act('unassign')">取消认领</button>
        <button v-if="selected.action_taken" class="admin-button" :disabled="busy" @click="act('reopen')">重新打开</button>
        <button v-else class="admin-button admin-button--primary" :disabled="busy" @click="act('resolve')">标记已处理</button>
      </div>
      <h3>关联帖子</h3>
      <p v-if="!selected.status_ids?.length" class="admin-empty">此举报未关联帖子</p>
      <p v-else-if="!selected.statuses?.length" class="admin-empty">关联帖子已删除或当前不可读取</p>
      <p v-else-if="selected.status_ids.length > (selected.statuses?.length || 0)" class="admin-empty">另有 {{ selected.status_ids.length - (selected.statuses?.length || 0) }} 条关联帖子已删除或不可读取</p>
      <article v-for="status in selected.statuses || []" :key="status.id" class="reported-status">
        <p><strong>{{ displayAccount(status.account?.acct) || '账号已不可用' }}</strong> · #{{ status.id }} · {{ status.visibility }}</p>
        <p v-if="status.spoiler_text">内容警告：{{ status.spoiler_text }}</p>
        <button class="admin-button admin-button--danger" :disabled="busy" @click="deleteStatus(status.id)">删除帖子</button>
        <RichText :html="status.content" :emojis="status.emojis" />
        <p v-if="status.media_attachments?.length">包含 {{ status.media_attachments.length }} 个附件</p>
      </article>
    </section>
  </section>
</template>
<style scoped>
.report-comment { white-space: pre-wrap; overflow-wrap: anywhere; }
.reported-status { border-top: 1px solid var(--color-separator); margin-top: 1rem; padding-top: 1rem; overflow-wrap: anywhere; }
</style>
