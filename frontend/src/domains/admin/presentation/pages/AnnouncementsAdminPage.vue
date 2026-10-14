<script setup lang="ts">
import { AdminAlert, AdminBadge, AdminButton, AdminCheck, AdminEmpty, AdminField, AdminPageHeader } from '../components/ui'
import { onMounted, reactive, ref } from 'vue'
import { announcementsAuditApi as api, type Announcement } from '../../infrastructure/announcementsAuditApi'
const rows = ref<Announcement[]>([])
const published = ref('')
const cursors = ref([''])
const selected = ref<string>()
const draft = reactive({ content: '', all_day: false, scheduled_at: '', starts_at: '', ends_at: '' })
const busy = ref(false)
const error = ref('')
function localTime(value: string | null) { if (!value) return ''; const d = new Date(value); return new Date(d.getTime() - d.getTimezoneOffset() * 60000).toISOString().slice(0, 16) }
function edit(row?: Announcement) {
  selected.value = row?.id
  Object.assign(draft, { content: row?.content || '', all_day: row?.all_day || false, scheduled_at: localTime(row?.scheduled_at || null), starts_at: localTime(row?.starts_at || null), ends_at: localTime(row?.ends_at || null) })
}
async function fetchRows() { rows.value = await api.list({ published: published.value || undefined, max_id: cursors.value.at(-1) || undefined }) }
async function run(work: () => Promise<void>) {
  if (busy.value) return
  busy.value = true; error.value = ''
  try { await work() } catch (e) { error.value = e instanceof Error ? e.message : '操作失败，请重试' } finally { busy.value = false }
}
function load(reset = false) { if (reset) cursors.value = ['']; return run(fetchRows) }
function save() { return run(async () => { const input = { ...draft, scheduled_at: draft.scheduled_at ? new Date(draft.scheduled_at).toISOString() : null, starts_at: draft.starts_at ? new Date(draft.starts_at).toISOString() : null, ends_at: draft.ends_at ? new Date(draft.ends_at).toISOString() : null }; await api.save(input, selected.value); edit(); cursors.value = ['']; await fetchRows() }) }
function toggle(row: Announcement) { if (!window.confirm(`确认${row.published ? '撤回' : '发布'}公告 #${row.id}？发布后仍受时间窗限制。`)) return; return run(async () => { await api.publish(row.id, !row.published); await fetchRows() }) }
function remove(row: Announcement) { if (!window.confirm(`永久删除公告 #${row.id}？此操作不可撤销。`)) return; return run(async () => { await api.remove(row.id); if (selected.value === row.id) edit(); await fetchRows() }) }
function page(next: boolean) { if (next) cursors.value.push(rows.value.at(-1)?.id || ''); else cursors.value.pop(); return load() }
onMounted(() => load())
</script>
<template>
  <section class="admin-page">
    <AdminPageHeader title="公告管理" description="纯文本公告，先存草稿再发布；时间按本地时区。" />
    <AdminAlert v-if="error" tone="danger">{{ error }}</AdminAlert>
    <form class="admin-card" @submit.prevent="save">
      <h2>{{ selected ? `编辑公告 #${selected}` : '新建公告' }}</h2>
      <AdminField label="内容"><template #default="{ id }"><textarea :id="id" v-model="draft.content" required maxlength="10000" rows="6" :disabled="busy" /></template></AdminField>
      <div class="admin-toolbar">
        <AdminField label="定时发布时间"><template #default="{ id }"><input :id="id" v-model="draft.scheduled_at" type="datetime-local" :disabled="busy" /></template></AdminField>
        <AdminField label="展示开始"><template #default="{ id }"><input :id="id" v-model="draft.starts_at" type="datetime-local" :disabled="busy" /></template></AdminField>
        <AdminField label="展示结束"><template #default="{ id }"><input :id="id" v-model="draft.ends_at" type="datetime-local" :disabled="busy" /></template></AdminField>
      </div>
      <AdminCheck v-model="draft.all_day" label="全天" :disabled="busy" />
      <div class="admin-form-actions">
        <AdminButton type="submit" variant="primary" :loading="busy">{{ selected ? '保存修改' : '保存草稿' }}</AdminButton>
        <AdminButton v-if="selected" :disabled="busy" @click="edit()">取消编辑</AdminButton>
      </div>
    </form>
    <form class="admin-toolbar" @submit.prevent="load(true)">
      <AdminField label="状态"><template #default="{ id }"><select :id="id" v-model="published"><option value="">全部</option><option value="false">草稿 / 已撤回</option><option value="true">已发布（含定时）</option></select></template></AdminField>
      <AdminButton type="submit" :disabled="busy">筛选</AdminButton>
    </form>
    <p v-if="busy" class="admin-empty" role="status">正在处理…</p>
    <article v-for="row in rows" :key="row.id" class="admin-card">
      <div class="admin-card__header"><h2>公告 #{{ row.id }}</h2><AdminBadge :tone="row.published ? 'success' : 'neutral'">{{ row.published ? '已发布（受时间窗限制）' : '草稿 / 已撤回' }}</AdminBadge></div>
      <p class="announcement-content">{{ row.content }}</p>
      <p>定时：{{ row.scheduled_at ? new Date(row.scheduled_at).toLocaleString() : '立即' }} · 开始：{{ row.starts_at ? new Date(row.starts_at).toLocaleString() : '不限' }} · 结束：{{ row.ends_at ? new Date(row.ends_at).toLocaleString() : '不限' }}</p>
      <div class="admin-actions">
        <AdminButton size="sm" :disabled="busy" @click="edit(row)">编辑</AdminButton>
        <AdminButton size="sm" :disabled="busy" @click="toggle(row)">{{ row.published ? '撤回' : '发布' }}</AdminButton>
        <AdminButton size="sm" variant="danger" :disabled="busy" @click="remove(row)">删除</AdminButton>
      </div>
    </article>
    <AdminEmpty v-if="!busy && !error && !rows.length">暂无公告</AdminEmpty>
    <nav class="admin-pagination" aria-label="公告分页">
      <AdminButton size="sm" :disabled="busy || cursors.length === 1" @click="page(false)">上一页</AdminButton>
      <AdminButton size="sm" :disabled="busy || rows.length < 40" @click="page(true)">下一页</AdminButton>
    </nav>
  </section>
</template>
<style scoped>
.announcement-content { white-space: pre-wrap; overflow-wrap: anywhere; }
</style>
