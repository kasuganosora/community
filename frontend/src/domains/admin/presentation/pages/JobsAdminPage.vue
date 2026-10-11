<script setup lang="ts">
import { onMounted, onBeforeUnmount, ref, watch } from 'vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { federationJobsApi as api, type Job } from '../../infrastructure/federationJobsApi'
const items = ref<Job[]>([]), page = ref(1), total = ref(0), kind = ref('delivery'), state = ref(''), busy = ref(false), error = ref(''), notice = ref('')
const labels: Record<string, string> = { pending: '待处理／等待重试', processing: '处理中', failed: '失败', complete: '已完成', delivered: '已投递', processed: '已展开', cancelled: '已取消' }
let version = 0
watch(sessionEpoch, () => { ++version; items.value = []; total.value = 0; notice.value = ''; busy.value = false }, { flush: 'sync' })
onBeforeUnmount(() => { ++version })
async function load(reset = false) {
  const mine = ++version
  if (reset) page.value = 1
  busy.value = true; error.value = ''
  try { const data = await api.jobs(page.value, kind.value, state.value); if (mine === version) { items.value = data.items; total.value = data.total } }
  catch { if (mine === version) error.value = '无法读取任务队列。' }
  finally { if (mine === version) busy.value = false }
}
function isFeed(job: Job) { return job.kind === 'feed-event' || job.kind === 'feed-batch' }
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
    await load()
  } catch { if (mine === version) error.value = '任务已变化、正在执行或操作失败，请刷新确认。' }
  finally { if (mine === version) busy.value = false }
}
onMounted(() => load())
</script>

<template>
  <div class="admin-page">
    <header><h1>联邦作业与任务队列</h1><p>查看联邦投递与 Feed 缓存任务。取消不能撤回已完成的投递，也不会删除帖子。</p></header>
    <form class="admin-card admin-toolbar" @submit.prevent="load(true)">
      <label class="admin-field">队列<select v-model="kind"><option value="delivery">ActivityPub 投递</option><option value="matrix-direct">私信桥接</option><option value="outbox">已提交联邦事件</option><option value="feed-event">Feed 事件</option><option value="feed-batch">Feed 受众批次</option></select></label>
      <label class="admin-field">状态<select v-model="state"><option value="">全部</option><option v-for="(label, key) in labels" :key="key" :value="key">{{ label }}</option></select></label>
      <button class="admin-button" :disabled="busy">查询／刷新</button>
    </form>
    <p v-if="kind.startsWith('feed-')" class="admin-alert">Feed 重试会恢复同一事件的未完成批次；取消任一批次会一并取消其事件。超过 7 天的任务仅按当前数据库重新协调，不重播历史内容。</p>
    <p v-if="kind === 'matrix-direct'" class="admin-alert">这里显示桥接处理状态，不代表远端服务器已经收到消息。只有消息作者可以在会话内按当前权限重试投递。</p>
    <p v-if="error" role="alert" class="admin-error">{{ error }}</p>
    <p v-if="notice" class="admin-alert admin-alert--success" role="status">{{ notice }}</p>
    <section class="admin-card" :aria-busy="busy">
      <p v-if="busy">加载中…</p><p v-else-if="!items.length" class="admin-empty">没有匹配的任务。</p>
      <article v-for="job in items" :key="`${job.kind}:${job.id}`" class="admin-card">
        <div class="admin-card__header"><strong>#{{ job.id }}</strong><span class="admin-badge" :class="{ 'admin-badge--success': ['delivered', 'processed', 'complete'].includes(job.state), 'admin-badge--danger': job.state === 'failed', 'admin-badge--warning': job.state === 'pending' }">{{ job.kind === 'matrix-direct' && job.state === 'delivered' ? '桥接处理完成' : labels[job.state] || job.state }}</span></div>
        <p>{{ job.event || job.activity_id }}</p><p v-if="job.parent_id">所属事件：#{{ job.parent_id }} · 批次 {{ job.batch_no }}</p>
        <p v-if="job.destination">目标：{{ job.destination }}</p>
        <p>尝试次数：{{ job.attempts }} <span v-if="job.next_attempt_at">计划时间：{{ new Date(job.next_attempt_at).toLocaleString() }}</span></p>
        <p v-if="job.lease_expires_at">执行租约截止：{{ new Date(job.lease_expires_at).toLocaleString() }}</p>
        <p v-if="job.requires_reconciliation" class="admin-alert">已超过 7 天重试期限，仅支持按当前数据库重新协调。</p>
        <p v-if="job.last_error" class="admin-error">{{ job.last_error }}</p>
        <div v-if="job.kind !== 'matrix-direct' && ['failed', 'pending'].includes(job.state) && job.can_retry !== false" class="admin-toolbar">
          <button class="admin-button" :disabled="busy" @click="act(job, 'retry')">{{ job.requires_reconciliation ? '重新协调缓存' : '立即重试' }}</button>
          <button class="admin-button admin-button--danger" :disabled="busy" @click="act(job, 'cancel')">取消任务</button>
        </div>
      </article>
    </section>
    <div class="admin-pagination"><button class="admin-button" :disabled="busy || page === 1" @click="page--; load()">上一页</button><span>第{{ page }}页，共{{ total }}项</span><button class="admin-button" :disabled="busy || page * 30 >= total" @click="page++; load()">下一页</button></div>
  </div>
</template>
