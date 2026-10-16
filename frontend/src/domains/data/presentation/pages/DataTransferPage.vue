<script setup lang="ts">
import { computed, onUnmounted, ref, watch } from 'vue'
import PrivateArchiveList from '../components/PrivateArchiveList.vue'
import { sessionEpoch } from '@/shared/api/sessionScope'
import { transferApi, type TransferJob, type TransferScope } from '../../infrastructure/transferApi'
import { t } from '@/shared/i18n'
const props = withDefaults(defineProps<{ scope?: TransferScope }>(), { scope: 'personal' })
const site = computed(() => props.scope === 'site')
const jobs = ref<TransferJob[]>([]), selected = ref<TransferJob | null>(null)
const source = ref('auto'), kind = ref('archive'), file = ref<File | null>(null)
const archiveOpen = ref(false)
const listLoading = ref(false), listError = ref(''), refreshing = ref(false)
let refreshVersion = 0
const busy = ref(false), error = ref(''), confirmation = ref(false), notice = ref('')
let generation = 0, controller = new AbortController(), timer: ReturnType<typeof setTimeout> | undefined
const states = { validating: t('data.DataTransferPage.s44'), ready: t('data.DataTransferPage.s45'), blocked: t('data.DataTransferPage.s46'), running: t('data.DataTransferPage.s47'), completed: t('data.DataTransferPage.s48'), failed: t('data.DataTransferPage.s49') }
const labels: Record<string, string> = { accounts: t('data.DataTransferPage.s50'), credentials: t('data.DataTransferPage.s51'), keys: t('data.DataTransferPage.s52'), posts: t('data.DataTransferPage.s15'), media: t('data.DataTransferPage.s53'), relations: t('data.DataTransferPage.s54'), lists: t('data.DataTransferPage.s21'), reactions: t('data.DataTransferPage.s55'), bookmarks: t('data.DataTransferPage.s20'), polls: t('data.DataTransferPage.s56'), poll_votes: t('data.DataTransferPage.s57'), emojis: t('data.DataTransferPage.s58'), records: t('data.DataTransferPage.s59'), archived_record: t('data.DataTransferPage.s59'), profile: t('data.DataTransferPage.s60'), credential: t('data.DataTransferPage.s51'), key: t('data.DataTransferPage.s52'), relation: t('data.DataTransferPage.s54'), emoji: t('data.DataTransferPage.s58'), post: t('data.DataTransferPage.s15'), account: t('data.DataTransferPage.s50') }
const canExecute = computed(() => !!selected.value?.report?.can_execute && ['ready', 'failed'].includes(selected.value.state))
function reportRows(values: Record<string, number> | null | undefined) {
  const rows: Record<string, number> = {}
  for (const [key, count] of Object.entries(values || {})) {
    const label = key.startsWith('archive:') ? t('data.DataTransferPage.s59') : labels[key] || labels[`${key}s`] || t('data.DataTransferPage.s61')
    rows[label] = (rows[label] || 0) + count
  }
  return rows
}
const reports = computed(() => selected.value?.report ? [
  { title: t('data.DataTransferPage.s62'), values: selected.value.report.counts }, { title: t('data.DataTransferPage.s63'), values: selected.value.report.created },
  { title: t('data.DataTransferPage.s64'), values: selected.value.report.skipped }, { title: t('data.DataTransferPage.s65'), values: selected.value.report.archived },
] : [])
function reset() { listLoading.value = false; listError.value = ''; refreshing.value = false; refreshVersion++; archiveOpen.value = false; generation++; controller.abort(); controller = new AbortController(); clearTimeout(timer); jobs.value = []; selected.value = null; file.value = null; busy.value = false; error.value = ''; confirmation.value = false; notice.value = '' }
function fail(e: unknown) {
  const status = (e as { response?: { status?: number } } | null)?.response?.status
  if (status === 401 || status === 403) reset()
  error.value = e instanceof Error ? e.message : t('data.DataTransferPage.s66') }
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
    if (version === generation && request === refreshVersion) { fail(e); notice.value = t('data.DataTransferPage.s67') }
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
  if (file.value && (file.value.size > 256 * 1024 * 1024 || (site.value && !file.value.name.toLowerCase().endsWith('.zip')))) { file.value = null; error.value = site.value ? t('data.DataTransferPage.s68') : t('data.DataTransferPage.s69') }
}
async function action(task: () => Promise<void>) { if (busy.value) return; busy.value = true; error.value = ''; const version = generation; try { await task() } catch (e) { if (version === generation) fail(e) } finally { if (version === generation) busy.value = false } }
function upload() { if (listLoading.value) return; const uploadFile = file.value, version = generation; if (!uploadFile) return; void action(async () => { const job = await transferApi.upload(props.scope, uploadFile, source.value, kind.value, controller.signal); if (version === generation) { file.value = null; put(job) } }) }
function execute() { const job = selected.value, version = generation; if (!job || !canExecute.value || !confirmation.value) return; void action(async () => {
  try { const result = await transferApi.execute(props.scope, job, controller.signal); if (version === generation) put(result) }
  catch (e) { if (version === generation) { confirmation.value = false; notice.value = t('data.DataTransferPage.s70'); await refresh() } throw e }
}) }
function remove() { const job = selected.value, version = generation; if (!job || job.state === 'running') return; void action(async () => { await transferApi.remove(props.scope, job.id, controller.signal); if (version === generation) { clearTimeout(timer); jobs.value = jobs.value.filter(j => j.id !== job.id); selected.value = null; notice.value = t('data.DataTransferPage.s71') } }) }
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
    <div class="page-header"><h1 class="page-header__title">{{ site ? $t('data.DataTransferPage.s1') : $t('data.DataTransferPage.s2') }}</h1></div>
    <div class="transfer-body">
      <p v-if="site">{{ $t('data.DataTransferPage.s3') }}</p>
      <template v-else><p>{{ $t('data.DataTransferPage.s4') }}</p><button :disabled="busy" @click="exportData">{{ $t('data.DataTransferPage.s5') }}</button><p>{{ $t('data.DataTransferPage.s6') }}</p></template>
      <template v-if="!site"><button @click="archiveOpen = !archiveOpen">{{ archiveOpen ? $t('data.DataTransferPage.s7') : $t('data.DataTransferPage.s8') }}</button><PrivateArchiveList v-if="archiveOpen" /></template>
      <p>{{ $t('data.DataTransferPage.s9') }}</p>
      <form :aria-label="$t('data.DataTransferPage.s10')" @submit.prevent="upload">
        <label>{{ $t('data.DataTransferPage.s11') }}<select v-model="source" :disabled="busy"><option value="auto">{{ $t('data.DataTransferPage.s12') }}</option><option value="community">Community</option><option value="misskey">Misskey</option><option value="mastodon">Mastodon</option></select></label>
        <label v-if="!site">{{ $t('data.DataTransferPage.s13') }}<select v-model="kind" :disabled="busy"><option value="archive">{{ $t('data.DataTransferPage.s14') }}</option><option value="notes">{{ $t('data.DataTransferPage.s15') }}</option><option value="following">{{ $t('data.DataTransferPage.s16') }}</option><option value="blocking">{{ $t('data.DataTransferPage.s17') }}</option><option value="muting">{{ $t('data.DataTransferPage.s18') }}</option><option value="domain_blocks">{{ $t('data.DataTransferPage.s19') }}</option><option value="bookmarks">{{ $t('data.DataTransferPage.s20') }}</option><option value="lists">{{ $t('data.DataTransferPage.s21') }}</option><option value="filters">{{ $t('data.DataTransferPage.s22') }}</option></select></label>
        <label>{{ $t('data.DataTransferPage.s23') }}<input type="file" :accept="site ? '.zip' : '.zip,.gz,.json,.csv'" :disabled="busy" @change="choose" /></label>
        <p v-if="file">{{ file.name }}</p><button type="submit" :disabled="busy || listLoading || !file">{{ $t('data.DataTransferPage.s24') }}</button>
      </form>
      <p v-if="busy" role="status">{{ $t('data.DataTransferPage.s25') }}</p><p v-if="error" role="alert">{{ error }}</p><p v-if="notice" role="status">{{ notice }}</p>
      <section :aria-label="$t('data.DataTransferPage.s26')" :aria-busy="listLoading"><h2>{{ $t('data.DataTransferPage.s26') }}</h2><button :disabled="busy || listLoading" @click="load">{{ listLoading ? $t('data.DataTransferPage.s27') : $t('data.DataTransferPage.s28') }}</button><p v-if="listLoading" role="status">{{ $t('data.DataTransferPage.s29') }}</p><p v-else-if="!listError && !jobs.length">{{ $t('data.DataTransferPage.s30') }}</p><ul><li v-for="job in jobs" :key="job.id"><button :disabled="busy" @click="put(job)">{{ job.filename }} · {{ states[job.state] }}</button></li></ul></section>
      <section v-if="selected" :aria-label="$t('data.DataTransferPage.s31')">
        <h2>{{ selected.filename }} · {{ states[selected.state] }}</h2>
        <p role="status">{{ selected.progress.phase }}：{{ selected.progress.completed }}<template v-if="selected.progress.total > 0"> / {{ selected.progress.total }}</template></p>
        <progress v-if="selected.progress.total > 0" :value="selected.progress.completed" :max="selected.progress.total" :aria-label="$t('data.DataTransferPage.s32')" />
        <p v-if="selected.error" role="alert">{{ selected.error }}</p>
        <template v-if="selected.report">
          <p>{{ $t('data.DataTransferPage.s33', [selected.report.source.platform, selected.report.source.base_url]) }}</p>
          <div v-for="group in reports" :key="group.title"><h3>{{ group.title }}</h3><dl><template v-for="(value, label) in reportRows(group.values)" :key="label"><dt>{{ label }}</dt><dd>{{ value }}</dd></template></dl></div>
          <ul><li v-for="(issue, index) in selected.report.issues" :key="index">{{ issue.severity === 'error' ? $t('data.DataTransferPage.s34') : $t('data.DataTransferPage.s35') }} · {{ issue.code }}：{{ issue.message }}</li></ul>
          <p>{{ $t('data.DataTransferPage.s36') }}</p>
          <button :disabled="busy" @click="downloadReport">{{ $t('data.DataTransferPage.s37') }}</button>
        </template>
        <template v-if="canExecute"><label><input v-model="confirmation" type="checkbox" :disabled="busy" />{{ $t('data.DataTransferPage.s38') }}</label><button :disabled="busy || !confirmation" @click="execute">{{ selected.state === 'failed' ? $t('data.DataTransferPage.s39') : $t('data.DataTransferPage.s40') }}</button></template>
        <button :disabled="busy || refreshing" :aria-busy="refreshing" @click="refresh">{{ refreshing ? $t('data.DataTransferPage.s41') : $t('data.DataTransferPage.s42') }}</button><button :disabled="busy || selected.state === 'running'" @click="remove">{{ $t('data.DataTransferPage.s43') }}</button>
      </section>
    </div>
  </div>
</template>
<style scoped>
.transfer-body{padding:var(--space-4) var(--gutter);display:flex;flex-direction:column;gap:var(--space-4);overflow-wrap:anywhere}.transfer-body form,.transfer-body section{display:flex;flex-direction:column;gap:var(--space-3);padding:var(--space-4);background:var(--color-bg-grouped);border-radius:var(--radius-md)}label{display:flex;gap:var(--space-2);align-items:center;flex-wrap:wrap}input,select,button{font:inherit;min-height:44px}button{padding:var(--space-2);border:1px solid var(--color-separator);border-radius:var(--radius-sm);text-align:left}button:disabled{opacity:.5}select{max-width:100%}input[type=file]{max-width:100%}dl{display:grid;grid-template-columns:1fr auto;gap:var(--space-2)}[role=alert]{color:var(--color-danger)}
</style>
