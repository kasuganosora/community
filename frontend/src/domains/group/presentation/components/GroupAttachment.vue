<script setup lang="ts">
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { groupApi } from '../../infrastructure/groupApi'
import { fileSize, type GroupAttachment } from '../../domain/group'

const props = defineProps<{ attachment: GroupAttachment; hidden?: boolean; sensitive?: boolean }>()
const root = ref<HTMLElement>()
const url = ref(''), loading = ref(false), error = ref(''), revealed = ref(false), inView = ref(false), previewable = ref(false)
const imageType = /^image\/(png|jpeg|gif|webp)$/
const image = computed(() => imageType.test(props.attachment.type))
const visible = computed(() => image.value && previewable.value && !!url.value && (!props.hidden || revealed.value))
let controller: AbortController | undefined
let observer: IntersectionObserver | undefined
let lastDownload = false

function reset() {
  controller?.abort(); controller = undefined
  if (url.value) URL.revokeObjectURL(url.value)
  url.value = ''; loading.value = false; error.value = ''; revealed.value = false; previewable.value = false
}
// A reused component or a newly applied sensitive flag must discard prior consent
// and any in-flight response, even when a mocked/remote transport ignores abort.
watch(() => [props.attachment.id, props.attachment.url, props.attachment.type, props.hidden], reset, { flush: 'sync' })

async function load(download = false) {
  if (loading.value) return
  lastDownload = download; loading.value = true; error.value = ''
  const request = new AbortController()
  controller = request
  const attachment = props.attachment
  try {
    if (!url.value) {
      const blob = await groupApi.media(attachment.url, request.signal)
      if (request.signal.aborted || controller !== request) return
      // Download other types as inert files; only allow-listed image responses
      // may be rendered. Never put a remote/private URL in an <img> element.
      previewable.value = imageType.test(attachment.type) && imageType.test(blob.type)
      url.value = URL.createObjectURL(new Blob([blob], { type: previewable.value ? blob.type : 'application/octet-stream' }))
    }
    if (download) {
      const link = document.createElement('a'); link.href = url.value; link.download = attachment.name; link.click()
    } else if (!previewable.value) error.value = '该附件无法预览，请下载查看。'
  } catch (e) {
    if (!request.signal.aborted && controller === request) error.value = e instanceof Error ? e.message : '附件加载失败'
  } finally {
    if (controller === request) loading.value = false
  }
}
function reveal() { revealed.value = true; void load() }
function retry() { if (lastDownload) void load(true); else reveal() }

// Images load automatically near the viewport, through the authenticated API.
// Hidden attachments never start a request until the viewer explicitly opts in.
watch(() => [inView.value, props.hidden, props.attachment.id, props.attachment.url, props.attachment.type], () => {
  if (inView.value && image.value && !props.hidden) void load()
})
onMounted(() => {
  if (typeof IntersectionObserver === 'undefined') { inView.value = true; return }
  observer = new IntersectionObserver(entries => {
    if (entries.some(entry => entry.isIntersecting)) { inView.value = true; observer?.disconnect() }
  }, { rootMargin: '240px' })
  if (root.value) observer.observe(root.value)
})
onBeforeUnmount(() => { observer?.disconnect(); reset() })
</script>
<template>
  <div ref="root" class="group-attachment">
    <button v-if="image && !visible" type="button" class="group-attachment__preview" :disabled="loading" @click="reveal"><t-icon :name="hidden ? 'browse-off' : 'image'" :size="28" /><span>{{ loading ? '正在加载图片…' : hidden ? (sensitive ? '敏感图片 · 点击查看' : '图片已隐藏 · 点击查看') : '查看图片' }}</span></button>
    <a v-if="visible" :href="url" target="_blank" rel="noopener noreferrer" aria-label="查看原图"><img :src="url" :alt="attachment.name" /></a>
    <div class="group-attachment__info"><span><strong>{{ attachment.name }}</strong><small>{{ fileSize(attachment.size) }} · {{ attachment.type || '文件' }}</small></span><button v-if="visible && hidden" type="button" class="btn btn--ghost btn--sm" @click="reset">隐藏</button><button type="button" class="btn btn--ghost btn--sm" :disabled="loading" @click="load(true)">{{ loading ? '加载中' : '下载' }}</button></div>
    <p v-if="error" role="alert">{{ error }} <button type="button" @click="retry">重试</button></p>
  </div>
</template>
<style scoped>
.group-attachment { margin-top:.65rem; max-width:28rem; min-width:0; border:1px solid var(--color-separator); border-radius:12px; overflow:hidden; }.group-attachment img { display:block; width:100%; max-width:100%; max-height:360px; aspect-ratio:4/3; object-fit:contain; background:var(--color-fill); }.group-attachment__preview { display:flex; align-items:center; justify-content:center; gap:.75rem; width:100%; min-height:110px; background:var(--color-fill); color:var(--color-label-secondary); }.group-attachment__info { display:flex; align-items:center; gap:.75rem; padding:.65rem .8rem; }.group-attachment__info>span { flex:1; min-width:0; }.group-attachment strong,.group-attachment small { display:block; overflow-wrap:anywhere; }.group-attachment strong { font-size:.85rem; }.group-attachment small { font-size:.75rem; color:var(--color-label-secondary); }.group-attachment p { padding:.7rem; font-size:.8rem; color:var(--color-danger); }
</style>
