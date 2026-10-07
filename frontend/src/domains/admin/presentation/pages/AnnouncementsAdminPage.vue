<script setup lang="ts">
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
    <header><h1>公告管理</h1><p>公告内容为纯文本。先保存草稿，再发布；开始、结束与定时发布时间均按本地时区输入。修改后用户会重新看到未读标记。</p></header>
    <p v-if="error" class="admin-error" role="alert">{{ error }}</p>
    <form class="admin-card announcement-form" @submit.prevent="save">
      <h2>{{ selected ? `编辑公告 #${selected}` : '新建公告' }}</h2>
      <label class="admin-field">内容<textarea v-model="draft.content" required maxlength="10000" rows="6" :disabled="busy" /></label>
      <div class="admin-toolbar">
        <label class="admin-field">定时发布时间<input v-model="draft.scheduled_at" type="datetime-local" :disabled="busy" /></label>
        <label class="admin-field">展示开始<input v-model="draft.starts_at" type="datetime-local" :disabled="busy" /></label>
        <label class="admin-field">展示结束<input v-model="draft.ends_at" type="datetime-local" :disabled="busy" /></label>
        <label><input v-model="draft.all_day" type="checkbox" :disabled="busy" />全天</label>
      </div>
      <div class="admin-toolbar"><button class="admin-button admin-button--primary" :disabled="busy">{{ selected ? '保存修改' : '保存草稿' }}</button><button v-if="selected" type="button" class="admin-button" :disabled="busy" @click="edit()">取消编辑</button></div>
    </form>
    <form class="admin-toolbar" @submit.prevent="load(true)"><label class="admin-field">状态<select v-model="published"><option value="">全部</option><option value="false">草稿 / 已撤回</option><option value="true">已发布（含定时）</option></select></label><button class="admin-button" :disabled="busy">筛选</button></form>
    <p v-if="busy" role="status">正在处理…</p>
    <article v-for="row in rows" :key="row.id" class="admin-card">
      <h2>公告 #{{ row.id }} <span class="admin-badge">{{ row.published ? '已发布（受时间窗限制）' : '草稿 / 已撤回' }}</span></h2>
      <p class="announcement-content">{{ row.content }}</p>
      <p>定时：{{ row.scheduled_at ? new Date(row.scheduled_at).toLocaleString() : '立即' }} · 开始：{{ row.starts_at ? new Date(row.starts_at).toLocaleString() : '不限' }} · 结束：{{ row.ends_at ? new Date(row.ends_at).toLocaleString() : '不限' }}</p>
      <div class="admin-toolbar"><button class="admin-button" :disabled="busy" @click="edit(row)">编辑</button><button class="admin-button" :disabled="busy" @click="toggle(row)">{{ row.published ? '撤回' : '发布' }}</button><button class="admin-button admin-button--danger" :disabled="busy" @click="remove(row)">删除</button></div>
    </article>
    <p v-if="!busy && !rows.length" class="admin-empty">暂无公告</p>
    <nav class="admin-pagination" aria-label="公告分页"><button class="admin-button" :disabled="busy || cursors.length === 1" @click="page(false)">上一页</button><button class="admin-button" :disabled="busy || rows.length < 40" @click="page(true)">下一页</button></nav>
  </section>
</template>
<style scoped>
.announcement-form { display: grid; gap: 16px; }
.announcement-content { white-space: pre-wrap; overflow-wrap: anywhere; }
textarea { width: 100%; box-sizing: border-box; }
</style>
