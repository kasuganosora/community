<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { federationJobsApi as api, type ErroredHost, type Job, type QueueStat } from '../../infrastructure/federationJobsApi'
import QueueSparkline from '../components/QueueSparkline.vue'

const QUEUES: { kind: string; label: string }[] = [
  { kind: 'delivery', label: 'ActivityPub 投递' },
  { kind: 'outbox', label: '联邦事件' },
  { kind: 'feed-event', label: 'Feed 事件' },
  { kind: 'feed-batch', label: 'Feed 受众批次' },
  { kind: 'matrix-direct', label: '私信桥接' },
]
const DONE: Record<string, string> = { delivery: 'delivered', outbox: 'processed', 'feed-event': 'complete', 'feed-batch': 'complete', 'matrix-direct': 'delivered' }
const STATUS_TABS = [
  { key: 'all', label: '全部' }, { key: 'latest', label: '最新' }, { key: 'done', label: '已完成' }, { key: 'failed', label: '失败' },
  { key: 'processing', label: '进行中' }, { key: 'delayed', label: '延迟' }, { key: 'waiting', label: '等待' },
]
const labels: Record<string, string> = { pending: '等待', processing: '进行中', failed: '失败', complete: '已完成', delivered: '已投递', processed: '已展开', cancelled: '已取消' }

const tab = ref('overview'), status = ref('all'), search = ref('')
const stats = ref<QueueStat[]>([]), hosts = ref<ErroredHost[]>([])
const items = ref<Job[]>([]), page = ref(1), total = ref(0), expanded = ref<Record<string, boolean>>({})
const busy = ref(false), error = ref(''), notice = ref('')
let version = 0, timer: ReturnType<typeof setInterval> | undefined

const queueLabel = (kind: string) => QUEUES.find(q => q.kind === kind)?.label ?? kind
const stat = computed(() => stats.value.find(s => s.kind === tab.value))
const supportsSearch = computed(() => tab.value === 'delivery' || tab.value === 'outbox')
const isFeed = (job: Job) => job.kind === 'feed-event' || job.kind === 'feed-batch'

function apiState(kind: string, key: string) {
  if (key === 'all' || key === 'latest') return ''
  if (key === 'done') return DONE[kind] ?? ''
  if ((key === 'delayed' || key === 'waiting') && !(kind === 'delivery' || kind === 'outbox')) return 'pending'
  return key
}
function displayState(job: Job) {
  if (job.state === 'pending' && job.next_attempt_at && new Date(job.next_attempt_at).getTime() > Date.now()) return { key: 'delayed', label: '延迟重试', icon: '◷' }
  if (job.kind === 'matrix-direct' && job.state === 'delivered') return { key: 'done', label: '桥接处理完成', icon: '✓' }
  const map: Record<string, string> = { pending: 'waiting', processing: 'processing', failed: 'failed', cancelled: 'cancelled' }
  const key = map[job.state] ?? 'done'
  const icon: Record<string, string> = { waiting: '○', processing: '●', failed: '✕', cancelled: '–', done: '✓' }
  return { key, label: labels[job.state] || job.state, icon: icon[key] }
}
function hostOf(job: Job) {
  if (!job.destination) return ''
  try { return new URL(job.destination).host } catch { return job.destination }
}
function relative(value?: string | null) {
  if (!value) return ''
  const t = new Date(value).getTime()
  if (Number.isNaN(t)) return ''
  const diff = (t - Date.now()) / 1000, abs = Math.abs(diff)
  const [n, unit] = abs < 60 ? [Math.round(abs), '秒'] : abs < 3600 ? [Math.round(abs / 60), '分钟'] : abs < 86400 ? [Math.round(abs / 3600), '小时'] : [Math.round(abs / 86400), '天']
  return diff > 0 ? `${n}${unit}后` : `${n}${unit}前`
}
const series = (s: QueueStat | undefined, key: 'processed' | 'failed' | 'active' | 'waiting' | 'delayed') => (s?.series ?? []).map(p => p[key])
const sum = (values: number[]) => values.reduce((a, b) => a + b, 0)

watch(sessionEpoch, () => { ++version; items.value = []; stats.value = []; hosts.value = []; total.value = 0; notice.value = ''; busy.value = false }, { flush: 'sync' })
onBeforeUnmount(() => { ++version; if (timer) clearInterval(timer) })

async function loadStats() {
  const mine = version
  try { const data = await api.queueStats(); if (mine === version) stats.value = data.queues }
  catch { if (mine === version && tab.value === 'overview') error.value = '无法读取队列统计。' }
}
async function loadHosts() {
  if (tab.value !== 'delivery') { hosts.value = []; return }
  const mine = version
  try { const data = await api.queueHosts(tab.value); if (mine === version) hosts.value = data.items }
  catch { if (mine === version) hosts.value = [] }
}
async function load(reset = false) {
  if (tab.value === 'overview') return
  const mine = ++version
  if (reset) { page.value = 1; expanded.value = {} }
  busy.value = true; error.value = ''
  try {
    const data = await api.jobs(page.value, tab.value, apiState(tab.value, status.value), supportsSearch.value ? search.value.trim() : '')
    if (mine === version) { items.value = data.items; total.value = data.total }
  } catch { if (mine === version) error.value = '无法读取任务队列。' }
  finally { if (mine === version) busy.value = false }
}
async function refresh(reset = false) { error.value = ''; await Promise.all([loadStats(), load(reset), loadHosts()]) }
function openTab(kind: string) { tab.value = kind; status.value = 'all'; search.value = ''; notice.value = ''; items.value = []; refresh(true) }
function setStatus(key: string) { status.value = key; load(true) }

function confirmation(job: Job, action: 'retry' | 'cancel') {
  if (isFeed(job)) {
    if (action === 'cancel') return '确认取消此 Feed 事件及全部尚未完成的依赖批次？任何正在执行的任务都会阻止取消。已完成的缓存更新不会撤回，业务帖子不会删除。'
    if (job.requires_reconciliation) return '此事件已超过 7 天重试期限。确认取消旧事件与未完成批次，并按当前数据库重新协调时间线缓存？不会重播历史帖子或发送历史通知。'
    return '确认恢复此 Feed 事件及其未完成批次？将继续已保存的扫描进度，正在执行的任务不会被重复启动。'
  }
  return action === 'cancel' ? '确认取消此待处理任务？该事件将不会继续投递，可能导致远端状态不同步。' : '确认立即重试此任务？将保留原活动 ID，正在执行的任务不会被重复启动。'
}
async function act(job: Job, action: 'retry' | 'cancel') {
  if (job.kind === 'matrix-direct' || busy.value || !window.confirm(confirmation(job, action))) return
  const mine = version
  busy.value = true; error.value = ''
  try {
    const result = await api.jobAction(job, action)
    if (mine !== version) return
    notice.value = result?.reconciled ? '已取消过期任务，并安排按当前数据库重新协调缓存。' : action === 'retry' ? '已安排重试。' : '任务已取消。'
    busy.value = false
    await refresh()
  } catch { if (mine === version) error.value = '任务已变化、正在执行或操作失败，请刷新确认。' }
  finally { if (mine === version) busy.value = false }
}
async function bulk(action: 'promote' | 'clear', host = '') {
  if (busy.value) return
  const scope = host ? `目标 ${host} 的` : `「${queueLabel(tab.value)}」队列中所有`
  const text = action === 'promote'
    ? `确认立即重试${scope}延迟任务？它们会在下一轮立即投递。`
    : `危险操作：确认清除${scope}等待、延迟与失败任务？这些任务将被标记为已取消且不会再投递，无法撤销，可能导致远端状态不同步。`
  if (!window.confirm(text)) return
  const mine = version
  busy.value = true; error.value = ''
  try {
    const result = await api.queueBulk(tab.value, action, host)
    if (mine !== version) return
    notice.value = action === 'promote' ? `已安排立即重试 ${result.updated} 个任务。` : `已清除 ${result.updated} 个任务。`
    busy.value = false
    await refresh(true)
  } catch { if (mine === version) error.value = '批量操作失败，请刷新确认。' }
  finally { if (mine === version) busy.value = false }
}

onMounted(() => {
  loadStats()
  timer = setInterval(() => { if (busy.value) return; loadStats(); if (status.value === 'latest' && tab.value !== 'overview') load() }, 15000)
})
</script>

<template>
  <div class="admin-page jobs-page">
    <header><h1>联邦作业与任务队列</h1><p>查看联邦投递与 Feed 缓存任务。取消不能撤回已完成的投递，也不会删除帖子。</p></header>

    <nav class="jobs-tabs" role="tablist" aria-label="队列">
      <button role="tab" :aria-selected="tab === 'overview'" :class="{ active: tab === 'overview' }" @click="openTab('overview')">概览</button>
      <button v-for="q in QUEUES" :key="q.kind" role="tab" :aria-selected="tab === q.kind" :class="{ active: tab === q.kind }" @click="openTab(q.kind)">{{ q.label }}</button>
    </nav>

    <p v-if="error" role="alert" class="admin-error">{{ error }}</p>
    <p v-if="notice" class="admin-alert admin-alert--success" role="status">{{ notice }}</p>

    <section v-if="tab === 'overview'" class="jobs-grid" aria-label="队列概览">
      <button v-for="s in stats" :key="s.kind" class="admin-card jobs-qcard" @click="openTab(s.kind)">
        <div class="jobs-qcard__head"><strong>{{ queueLabel(s.kind) }}</strong><span class="jobs-muted">{{ s.label }}</span></div>
        <dl class="jobs-counts">
          <div><dt>进行中</dt><dd>{{ s.counts.active }}</dd></div>
          <div><dt>延迟</dt><dd :class="{ warn: s.counts.delayed }">{{ s.counts.delayed }}</dd></div>
          <div><dt>等待</dt><dd>{{ s.counts.waiting }}</dd></div>
          <div><dt>失败</dt><dd :class="{ bad: s.counts.failed }">{{ s.counts.failed }}</dd></div>
        </dl>
        <QueueSparkline :label="`${queueLabel(s.kind)} 每分钟处理与失败`" :series="[{ values: series(s, 'processed'), tone: 'accent' }, { values: series(s, 'failed'), tone: 'danger' }]" />
        <div class="jobs-legend"><span class="dot accent" />处理 {{ sum(series(s, 'processed')) }}<span class="dot danger" />失败 {{ sum(series(s, 'failed')) }}<span class="jobs-muted">· 近 {{ s.series.length }} 分钟</span></div>
      </button>
      <article class="admin-card jobs-qcard jobs-qcard--static">
        <div class="jobs-qcard__head"><strong>ActivityPub 收件</strong><span class="jobs-muted">inbox</span></div>
        <p class="jobs-muted">收件箱活动在请求内同步处理，没有持久化队列。</p>
      </article>
      <p v-if="!stats.length && !error" class="admin-empty">加载中…</p>
    </section>

    <template v-else>
      <section v-if="stat" class="admin-card jobs-detail-head">
        <dl class="jobs-counts jobs-counts--wide">
          <div><dt>进行中</dt><dd>{{ stat.counts.active }}</dd></div>
          <div><dt>延迟</dt><dd :class="{ warn: stat.counts.delayed }">{{ stat.counts.delayed }}</dd></div>
          <div><dt>等待</dt><dd>{{ stat.counts.waiting }}</dd></div>
          <div><dt>失败</dt><dd :class="{ bad: stat.counts.failed }">{{ stat.counts.failed }}</dd></div>
          <div><dt>已完成</dt><dd>{{ stat.counts.completed }}</dd></div>
        </dl>
        <div class="jobs-charts">
          <figure><figcaption>处理 / 失败</figcaption><QueueSparkline label="每分钟处理与失败" :height="56" :series="[{ values: series(stat, 'processed'), tone: 'accent' }, { values: series(stat, 'failed'), tone: 'danger' }]" /></figure>
          <figure><figcaption>进行中</figcaption><QueueSparkline label="进行中" :height="56" :series="[{ values: series(stat, 'active'), tone: 'accent' }]" /></figure>
          <figure><figcaption>等待</figcaption><QueueSparkline label="等待" :height="56" :series="[{ values: series(stat, 'waiting'), tone: 'muted' }]" /></figure>
          <figure><figcaption>延迟</figcaption><QueueSparkline label="延迟" :height="56" :series="[{ values: series(stat, 'delayed'), tone: 'warning' }]" /></figure>
        </div>
        <div v-if="stat.bulk_actions" class="admin-toolbar jobs-bulk">
          <button class="admin-button" :disabled="busy || !stat.counts.delayed" @click="bulk('promote')">立刻重试所有</button>
          <button class="admin-button admin-button--danger" :disabled="busy || !(stat.counts.waiting + stat.counts.delayed + stat.counts.failed)" @click="bulk('clear')">清除队列</button>
        </div>
      </section>

      <section v-if="tab === 'delivery'" class="admin-card" aria-label="出错实例">
        <div class="admin-card__header"><strong>出错实例</strong><span class="jobs-muted">按目标主机汇总失败与延迟重试</span></div>
        <p v-if="!hosts.length" class="admin-empty">没有出错的实例。</p>
        <ul v-else class="jobs-hosts">
          <li v-for="h in hosts" :key="h.host">
            <div class="jobs-hosts__main"><strong>{{ h.host }}</strong><span v-if="h.last_error" class="jobs-muted jobs-ellipsis">{{ h.last_error }}</span></div>
            <span class="admin-badge admin-badge--danger" title="失败">{{ h.failed }}</span>
            <span class="admin-badge admin-badge--warning" title="延迟">{{ h.delayed }}</span>
            <button class="admin-button admin-button--sm" :disabled="busy || !h.delayed" @click="bulk('promote', h.host)">重试</button>
            <button class="admin-button admin-button--sm admin-button--danger" :disabled="busy" @click="bulk('clear', h.host)">清除</button>
          </li>
        </ul>
      </section>

      <p v-if="tab.startsWith('feed-')" class="admin-alert">Feed 重试会恢复同一事件的未完成批次；取消任一批次会一并取消其事件。超过 7 天的任务仅按当前数据库重新协调，不重播历史内容。</p>
      <p v-if="tab === 'matrix-direct'" class="admin-alert">这里显示桥接处理状态，不代表远端服务器已经收到消息。只有消息作者可以在会话内按当前权限重试投递。</p>

      <section class="admin-card" :aria-busy="busy">
        <div class="jobs-filter">
          <div class="jobs-status" role="tablist" aria-label="状态">
            <button v-for="s in STATUS_TABS" :key="s.key" role="tab" :aria-selected="status === s.key" :class="{ active: status === s.key }" @click="setStatus(s.key)">{{ s.label }}</button>
          </div>
          <form v-if="supportsSearch" class="jobs-search" @submit.prevent="load(true)">
            <input v-model="search" type="search" :placeholder="tab === 'delivery' ? '搜索目标主机或活动 ID' : '搜索事件类型'" aria-label="搜索任务">
            <button class="admin-button admin-button--sm" :disabled="busy">搜索</button>
          </form>
        </div>
        <p v-if="busy && !items.length">加载中…</p><p v-else-if="!error && !items.length" class="admin-empty">没有匹配的任务。</p>
        <ol class="jobs-timeline">
          <li v-for="job in items" :key="`${job.kind}:${job.id}`" :class="`is-${displayState(job).key}`">
            <button class="jobs-row" :aria-expanded="!!expanded[job.kind + job.id]" @click="expanded[job.kind + job.id] = !expanded[job.kind + job.id]">
              <span class="jobs-row__icon" :title="displayState(job).label" aria-hidden="true">{{ displayState(job).icon }}</span>
              <span class="jobs-row__id">#{{ job.id }}</span>
              <span class="jobs-row__type">{{ job.activity_type || job.event || job.activity_id || job.kind }}</span>
              <span class="jobs-row__host jobs-ellipsis">{{ hostOf(job) || (job.parent_id ? `事件 #${job.parent_id} · 批次 ${job.batch_no}` : '') }}</span>
              <span class="jobs-row__state">{{ displayState(job).label }}</span>
              <time class="jobs-row__time" :datetime="job.created_at">{{ relative(job.created_at) }}</time>
            </button>
            <div v-if="expanded[job.kind + job.id]" class="jobs-row__detail">
              <dl>
                <div v-if="job.activity_id"><dt>活动</dt><dd class="jobs-break">{{ job.activity_id }}</dd></div>
                <div v-if="job.event"><dt>事件</dt><dd>{{ job.event }}</dd></div>
                <div v-if="job.parent_id"><dt>所属事件</dt><dd>所属事件：#{{ job.parent_id }} · 批次 {{ job.batch_no }}</dd></div>
                <div v-if="job.destination"><dt>目标</dt><dd class="jobs-break">{{ job.destination }}</dd></div>
                <div><dt>尝试次数</dt><dd>{{ job.attempts }}</dd></div>
                <div v-if="job.next_attempt_at"><dt>计划时间</dt><dd>{{ new Date(job.next_attempt_at).toLocaleString() }}（{{ relative(job.next_attempt_at) }}）</dd></div>
                <div v-if="job.lease_expires_at"><dt>租约截止</dt><dd>{{ new Date(job.lease_expires_at).toLocaleString() }}</dd></div>
                <div v-if="job.created_at"><dt>创建</dt><dd>{{ new Date(job.created_at).toLocaleString() }}</dd></div>
              </dl>
              <p v-if="job.requires_reconciliation" class="admin-alert">已超过 7 天重试期限，仅支持按当前数据库重新协调。</p>
              <p v-if="job.last_error" class="admin-error">{{ job.last_error }}</p>
              <div v-if="job.kind !== 'matrix-direct' && ['failed', 'pending'].includes(job.state) && job.can_retry !== false" class="admin-toolbar">
                <button class="admin-button admin-button--sm" :disabled="busy" @click="act(job, 'retry')">{{ job.requires_reconciliation ? '重新协调缓存' : '立即重试' }}</button>
                <button class="admin-button admin-button--sm admin-button--danger" :disabled="busy" @click="act(job, 'cancel')">取消任务</button>
              </div>
            </div>
          </li>
        </ol>
      </section>
      <div class="admin-pagination"><button class="admin-button" :disabled="busy || page === 1" @click="page--; load()">上一页</button><span>第{{ page }}页，共{{ total }}项</span><button class="admin-button" :disabled="busy || page * 30 >= total" @click="page++; load()">下一页</button></div>
    </template>
  </div>
</template>

<style scoped>
.jobs-tabs, .jobs-status { display: flex; gap: 4px; overflow-x: auto; scrollbar-width: none; }
.jobs-tabs { border-bottom: 1px solid var(--color-separator); margin-bottom: 16px; }
.jobs-tabs button, .jobs-status button { flex: none; background: none; border: 0; color: var(--color-label-secondary); font: inherit; cursor: pointer; white-space: nowrap; }
.jobs-tabs button { padding: 10px 12px; border-bottom: 2px solid transparent; margin-bottom: -1px; }
.jobs-tabs button.active { color: var(--admin-accent); border-bottom-color: var(--admin-accent); font-weight: 600; }
.jobs-status button { padding: 5px 10px; border-radius: 999px; font-size: 13px; }
.jobs-status button.active { background: color-mix(in srgb, var(--admin-accent) 12%, transparent); color: var(--admin-accent); font-weight: 600; }
.jobs-grid { display: grid; grid-template-columns: repeat(auto-fill, minmax(240px, 1fr)); gap: 12px; }
.jobs-qcard { display: flex; flex-direction: column; gap: 10px; text-align: left; font: inherit; color: inherit; cursor: pointer; margin: 0; }
.jobs-qcard--static { cursor: default; }
.jobs-qcard:not(.jobs-qcard--static):hover { border-color: var(--admin-accent); }
.jobs-qcard__head { display: flex; justify-content: space-between; gap: 8px; align-items: baseline; }
.jobs-muted { color: var(--color-label-tertiary); font-size: 12px; }
.jobs-counts { display: grid; grid-template-columns: repeat(4, 1fr); gap: 6px; margin: 0; }
.jobs-counts--wide { grid-template-columns: repeat(5, 1fr); }
.jobs-counts div { min-width: 0; }
.jobs-counts dt { font-size: 11px; color: var(--color-label-secondary); }
.jobs-counts dd { margin: 0; font-size: 20px; font-weight: 600; font-variant-numeric: tabular-nums; }
.jobs-counts dd.warn { color: var(--color-warning, #ff9500); }
.jobs-counts dd.bad { color: var(--color-danger); }
.jobs-legend { display: flex; align-items: center; gap: 6px; font-size: 12px; color: var(--color-label-secondary); }
.dot { width: 8px; height: 8px; border-radius: 50%; display: inline-block; }
.dot.accent { background: var(--admin-accent); } .dot.danger { background: var(--color-danger); margin-left: 6px; }
.jobs-detail-head { display: flex; flex-direction: column; gap: 14px; }
.jobs-charts { display: grid; grid-template-columns: repeat(4, 1fr); gap: 12px; }
.jobs-charts figure { margin: 0; }
.jobs-charts figcaption { font-size: 12px; color: var(--color-label-secondary); margin-bottom: 4px; }
.jobs-hosts { list-style: none; margin: 0; padding: 0; }
.jobs-hosts li { display: flex; align-items: center; gap: 8px; padding: 8px 0; border-top: 1px solid var(--color-separator); }
.jobs-hosts__main { flex: 1; min-width: 0; display: flex; flex-direction: column; }
.jobs-ellipsis { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.jobs-break { word-break: break-all; }
.jobs-filter { display: flex; justify-content: space-between; gap: 12px; flex-wrap: wrap; margin-bottom: 8px; }
.jobs-search { display: flex; gap: 6px; flex: 1 1 220px; max-width: 360px; }
.jobs-search input { flex: 1; min-width: 0; height: 32px; padding: 0 10px; border-radius: 8px; border: 1px solid var(--color-separator); background: var(--admin-control-bg); color: var(--color-label); font: inherit; }
.jobs-timeline { list-style: none; margin: 0; padding: 0; }
.jobs-timeline li { border-top: 1px solid var(--color-separator); }
.jobs-row { width: 100%; display: grid; grid-template-columns: 20px 48px minmax(80px, 160px) minmax(0, 1fr) 72px 72px; gap: 10px; align-items: center; padding: 9px 4px; background: none; border: 0; color: inherit; font: inherit; text-align: left; cursor: pointer; }
.jobs-row:hover { background: var(--color-fill); }
.jobs-timeline .jobs-row { justify-content: stretch; justify-items: start; }
.jobs-timeline .jobs-row > * { justify-self: stretch; text-align: left; min-width: 0; }
.jobs-timeline .jobs-row > .jobs-row__time { text-align: right; }
.jobs-row__icon { text-align: center; font-weight: 700; color: var(--color-label-tertiary); }
.is-failed .jobs-row__icon, .is-failed .jobs-row__state { color: var(--color-danger); }
.is-delayed .jobs-row__icon, .is-delayed .jobs-row__state { color: var(--color-warning, #ff9500); }
.is-processing .jobs-row__icon { color: var(--admin-accent); }
.is-done .jobs-row__icon { color: var(--color-success); }
.jobs-row__id { font-variant-numeric: tabular-nums; color: var(--color-label-secondary); font-size: 13px; }
.jobs-row__type { font-weight: 600; overflow: hidden; text-overflow: ellipsis; white-space: nowrap; }
.jobs-row__host { color: var(--color-label-secondary); font-size: 13px; }
.jobs-row__state { font-size: 12px; color: var(--color-label-secondary); }
.jobs-row__time { font-size: 12px; color: var(--color-label-tertiary); text-align: right; }
.jobs-row__detail { padding: 4px 4px 12px 34px; display: flex; flex-direction: column; gap: 8px; }
.jobs-row__detail > dl { margin: 0; display: flex !important; flex-direction: column; gap: 4px; font-size: 13px; }
.jobs-row__detail dl div { display: grid; grid-template-columns: 72px 1fr; gap: 8px; }
.jobs-row__detail dt { color: var(--color-label-secondary); }
.jobs-row__detail dd { margin: 0; }
@media (max-width: 640px) {
  .jobs-charts { grid-template-columns: repeat(2, 1fr); }
  .jobs-counts--wide { grid-template-columns: repeat(3, 1fr); }
  .jobs-row { grid-template-columns: 18px minmax(0, 1fr) auto; row-gap: 2px; }
  .jobs-row__id, .jobs-row__state { display: none; }
  .jobs-row__host { grid-column: 2 / 4; grid-row: 2; }
  .jobs-row__detail { padding-left: 4px; }
}
</style>
