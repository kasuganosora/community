<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import PrivateArchiveList from '../components/PrivateArchiveList.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { transferApi, type TransferJob, type TransferScope } from '../../infrastructure/transferApi'
const props = withDefaults(defineProps<{ scope?: TransferScope }>(), { scope: 'personal' })
const site = computed(() => props.scope === 'site')
const jobs = ref<TransferJob[]>([]), selected = ref<TransferJob | null>(null)
const source = ref('auto'), kind = ref('archive'), file = ref<File | null>(null)
const archiveOpen = ref(false)
const listLoading = ref(false), listError = ref(''), refreshing = ref(false)
let refreshVersion = 0
const busy = ref(false), error = ref(''), confirmation = ref(false), notice = ref('')
let generation = 0, controller = new AbortController(), timer: ReturnType<typeof setTimeout> | undefined
const states = { validating: '预检中', ready: '等待确认', blocked: '预检未通过', running: '导入中', completed: '已完成', failed: '失败' }
const labels: Record<string, string> = { accounts: '账号', credentials: '登录凭据', keys: '联邦密钥', posts: '帖子', media: '媒体', relations: '关系', lists: '列表', reactions: '回应', bookmarks: '书签', polls: '投票', poll_votes: '投票记录', emojis: '表情', records: '保留记录', archived_record: '保留记录', profile: '个人资料', credential: '登录凭据', key: '联邦密钥', relation: '关系', emoji: '表情', post: '帖子', account: '账号' }
const canExecute = computed(() => !!selected.value?.report?.can_execute && ['ready', 'failed'].includes(selected.value.state))
function reportRows(values: Record<string, number> | null | undefined) {
  const rows: Record<string, number> = {}
  for (const [key, count] of Object.entries(values || {})) {
    const label = key.startsWith('archive:') ? '保留记录' : labels[key] || labels[`${key}s`] || '其他记录'
    rows[label] = (rows[label] || 0) + count
  }
  return rows
}
const reports = computed(() => selected.value?.report ? [
  { title: '检测到', values: selected.value.report.counts }, { title: '已创建', values: selected.value.report.created },
  { title: '重复跳过', values: selected.value.report.skipped }, { title: '仅归档', values: selected.value.report.archived },
] : [])
function reset() { listLoading.value = false; listError.value = ''; refreshing.value = false; refreshVersion++; archiveOpen.value = false; generation++; controller.abort(); controller = new AbortController(); clearTimeout(timer); jobs.value = []; selected.value = null; file.value = null; busy.value = false; error.value = ''; confirmation.value = false; notice.value = '' }
function fail(e: unknown) {
  const status = (e as { response?: { status?: number } } | null)?.response?.status
  if (status === 401 || status === 403) reset()
  error.value = e instanceof Error ? e.message : '操作失败，请稍后重试' }
function put(job: TransferJob) { refreshVersion++; refreshing.value = false; selected.value = job; jobs.value = [job, ...jobs.value.filter(j => j.id !== job.id)]; confirmation.value = false; schedule() }
function schedule() { clearTimeout(timer); if (selected.value && ['validating', 'running'].includes(selected.value.state)) timer = setTimeout(() => { void refresh() }, 2500) }
async function refresh() {
  const job = selected.value, version = generation
  if (!job || refreshing.value) return
  const request = ++refreshVersion
  refreshing.value = true; error.value = ''
  try {
    const result = await transferApi.get(props.scope, job.id, controller.signal)
    if (version === generation && request === refreshVersion && selected.value?.id === job.id) put(result)
  } catch (e) {
    if (version === generation && request === refreshVersion) { fail(e); notice.value = '连接中断不会取消服务器任务，可点击刷新恢复进度。' }
  } finally {
    if (version === generation && request === refreshVersion) refreshing.value = false
  }
}
async function load() {
  if (listLoading.value || busy.value) return
  const version = generation
  listLoading.value = true; listError.value = ''; error.value = ''
  try {
    const result = await transferApi.list(props.scope, controller.signal)
    if (version === generation) jobs.value = result
  } catch (e) {
    if (version === generation) { fail(e); listError.value = error.value }
  } finally {
    if (version === generation) listLoading.value = false
  }
}
function choose(event: Event) {
  const input = event.target as HTMLInputElement
  file.value = input.files?.[0] || null; input.value = ''; confirmation.value = false; error.value = ''
  if (file.value && (file.value.size > 256 * 1024 * 1024 || (site.value && !file.value.name.toLowerCase().endsWith('.zip')))) { file.value = null; error.value = site.value ? '请选择不超过 256 MiB 的整站迁移 ZIP 包' : '文件超过 256 MiB，请使用命令行导入' }
}
async function action(task: () => Promise<void>) { if (busy.value) return; busy.value = true; error.value = ''; const version = generation; try { await task() } catch (e) { if (version === generation) fail(e) } finally { if (version === generation) busy.value = false } }
function upload() { if (listLoading.value) return; const uploadFile = file.value, version = generation; if (!uploadFile) return; void action(async () => { const job = await transferApi.upload(props.scope, uploadFile, source.value, kind.value, controller.signal); if (version === generation) { file.value = null; put(job) } }) }
function execute() { const job = selected.value, version = generation; if (!job || !canExecute.value || !confirmation.value) return; void action(async () => {
  try { const result = await transferApi.execute(props.scope, job, controller.signal); if (version === generation) put(result) }
  catch (e) { if (version === generation) { confirmation.value = false; notice.value = '执行请求结果不确定时请刷新此任务，不要重新上传。'; await refresh() } throw e }
}) }
function remove() { const job = selected.value, version = generation; if (!job || job.state === 'running') return; void action(async () => { await transferApi.remove(props.scope, job.id, controller.signal); if (version === generation) { clearTimeout(timer); jobs.value = jobs.value.filter(j => j.id !== job.id); selected.value = null; notice.value = '已删除任务私有包；已导入的数据不会撤销。' } }) }
function download(blob: Blob, name: string) { const url = URL.createObjectURL(blob), a = document.createElement('a'); a.href = url; a.download = name; a.click(); setTimeout(() => URL.revokeObjectURL(url), 1000) }
function exportData() { const version = generation; void action(async () => { const blob = await transferApi.export(controller.signal); if (version === generation) download(blob, 'community-personal-backup.zip') }) }
function downloadReport() { const job = selected.value, version = generation; if (!job) return; void action(async () => { const report = await transferApi.report(props.scope, job.id, controller.signal); if (version === generation) download(new Blob([JSON.stringify(report, null, 2)], { type: 'application/json' }), 'import-report.json') }) }
watch([source, kind], () => { confirmation.value = false })
watch(sessionEpoch, reset, { flush: 'sync' })
watch(() => props.scope, () => { reset(); void load() }, { immediate: true })
onUnmounted(reset)
</script>

<template>
  <div class="transfer-page">
    <div class="page-header"><h1 class="page-header__title">{{ site ? '整站迁移' : '个人数据导入与导出' }}</h1></div>
    <div class="transfer-body">
      <p v-if="site">仅用于同域名迁移。请使用仓库 scripts/migration 中的源数据库导出器生成 ZIP，包含数据库记录、媒体与联邦密钥；个人备份不能代替整站包。页面只显示统计，不展示密码或私钥。</p>
      <template v-else><p>支持 Community 个人 ZIP、Mastodon ZIP / tar.gz、Misskey JSON 和支持的 CSV。不会覆盖当前登录身份，也不会重新向联邦发布历史消息。收件人无法解析的私密内容仅归档，请查看报告。</p><button :disabled="busy" @click="exportData">导出我的个人备份</button><p>导出文件可能含私密数据，请妥善保存。此处不接受整站数据库包、其他账户凭据或联邦私钥。</p></template>
      <template v-if="!site"><button @click="archiveOpen = !archiveOpen">{{ archiveOpen ? '收起私有备份内容' : '查看私有备份内容' }}</button><PrivateArchiveList v-if="archiveOpen" /></template>
      <p>上传限 256 MiB，解压限 1 GiB；更大的数据请使用命令行工具。</p>
      <form aria-label="导入预检" @submit.prevent="upload">
        <label>来源<select v-model="source" :disabled="busy"><option value="auto">自动识别</option><option value="community">Community</option><option value="misskey">Misskey</option><option value="mastodon">Mastodon</option></select></label>
        <label v-if="!site">内容类型<select v-model="kind" :disabled="busy"><option value="archive">完整个人备份</option><option value="notes">帖子</option><option value="following">关注</option><option value="blocking">屏蔽</option><option value="muting">静音</option><option value="domain_blocks">域名屏蔽</option><option value="bookmarks">书签</option><option value="lists">列表</option><option value="filters">过滤规则</option></select></label>
        <label>备份文件<input type="file" :accept="site ? '.zip' : '.zip,.gz,.json,.csv'" :disabled="busy" @change="choose" /></label>
        <p v-if="file">{{ file.name }}</p><button type="submit" :disabled="busy || listLoading || !file">上传并预检</button>
      </form>
      <p v-if="busy" role="status">正在处理请求，请稍候…</p><p v-if="error" role="alert">{{ error }}</p><p v-if="notice" role="status">{{ notice }}</p>
      <section aria-label="导入任务" :aria-busy="listLoading"><h2>导入任务</h2><button :disabled="busy || listLoading" @click="load">{{ listLoading ? '读取中…' : '刷新任务列表' }}</button><p v-if="listLoading" role="status">正在读取任务…</p><p v-else-if="!listError && !jobs.length">暂无任务</p><ul><li v-for="job in jobs" :key="job.id"><button :disabled="busy" @click="put(job)">{{ job.filename }} · {{ states[job.state] }}</button></li></ul></section>
      <section v-if="selected" aria-label="预检与执行报告">
        <h2>{{ selected.filename }} · {{ states[selected.state] }}</h2>
        <p role="status">{{ selected.progress.phase }}：{{ selected.progress.completed }}<template v-if="selected.progress.total > 0"> / {{ selected.progress.total }}</template></p>
        <progress v-if="selected.progress.total > 0" :value="selected.progress.completed" :max="selected.progress.total" aria-label="导入进度" />
        <p v-if="selected.error" role="alert">{{ selected.error }}</p>
        <template v-if="selected.report">
          <p>来源：{{ selected.report.source.platform }} {{ selected.report.source.base_url }}</p>
          <div v-for="group in reports" :key="group.title"><h3>{{ group.title }}</h3><dl><template v-for="(value, label) in reportRows(group.values)" :key="label"><dt>{{ label }}</dt><dd>{{ value }}</dd></template></dl></div>
          <ul><li v-for="(issue, index) in selected.report.issues" :key="index">{{ issue.severity === 'error' ? '阻止导入' : '提示' }} · {{ issue.code }}：{{ issue.message }}</li></ul>
          <p>重复记录会跳过；“仅归档”表示保留了源数据，不代表已转换为可用功能。历史内容不会重新广播。</p>
          <button :disabled="busy" @click="downloadReport">下载报告</button>
        </template>
        <template v-if="canExecute"><label><input v-model="confirmation" type="checkbox" :disabled="busy" />我已核对预检报告，确认按此版本导入</label><button :disabled="busy || !confirmation" @click="execute">{{ selected.state === 'failed' ? '重试此任务' : '确认执行导入' }}</button></template>
        <button :disabled="busy || refreshing" :aria-busy="refreshing" @click="refresh">{{ refreshing ? '刷新中…' : '刷新任务状态' }}</button><button :disabled="busy || selected.state === 'running'" @click="remove">删除任务私有包</button>
      </section>
    </div>
  </div>
</template>
<style scoped>
.transfer-body{padding:var(--space-4) var(--gutter);display:flex;flex-direction:column;gap:var(--space-4);overflow-wrap:anywhere}.transfer-body form,.transfer-body section{display:flex;flex-direction:column;gap:var(--space-3);padding:var(--space-4);background:var(--color-bg-grouped);border-radius:var(--radius-md)}label{display:flex;gap:var(--space-2);align-items:center;flex-wrap:wrap}input,select,button{font:inherit;min-height:44px}button{padding:var(--space-2);border:1px solid var(--color-separator);border-radius:var(--radius-sm);text-align:left}button:disabled{opacity:.5}select{max-width:100%}input[type=file]{max-width:100%}dl{display:grid;grid-template-columns:1fr auto;gap:var(--space-2)}[role=alert]{color:var(--color-danger)}
</style>
