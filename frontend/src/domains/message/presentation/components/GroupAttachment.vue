<script setup lang="ts">
import { computed, onBeforeUnmount, ref } from 'vue'
import { groupApi } from '../../infrastructure/groupApi'
import { fileSize, type GroupAttachment } from '../../domain/group'
const props = defineProps<{ attachment: GroupAttachment }>()
const url = ref(''), loading = ref(false), error = ref('')
const image = computed(() => /^image\/(png|jpeg|gif|webp)$/.test(props.attachment.type))
let lastDownload = false
const controller = new AbortController()
async function load(download = false) {
  if (loading.value) return
  lastDownload = download; loading.value = true; error.value = ''
  try {
    if (!url.value) {
      const blob = await groupApi.media(props.attachment.url, controller.signal)
      if (controller.signal.aborted) return
      // Only explicitly allow-listed image types can be displayed inline.
      const safeType = image.value && /^image\/(png|jpeg|gif|webp)$/.test(blob.type) ? blob.type : 'application/octet-stream'
      url.value = URL.createObjectURL(new Blob([blob], { type: safeType }))
    }
    if (download) { const link = document.createElement('a'); link.href = url.value; link.download = props.attachment.name; link.click() }
  } catch (e) { if (!controller.signal.aborted) error.value = e instanceof Error ? e.message : '附件加载失败' }
  finally { loading.value = false }
}
// Fetch images only on demand: remote/private files never leak through a public image URL.
onBeforeUnmount(() => { controller.abort(); if (url.value) URL.revokeObjectURL(url.value) })
</script>
<template>
  <div class="group-attachment">
    <button v-if="image && !url" type="button" class="group-attachment__preview" :disabled="loading" @click="load()"><t-icon name="image" :size="28" /><span>{{ loading ? '正在加载图片…' : '查看图片' }}</span></button>
    <a v-if="image && url" :href="url" target="_blank" rel="noopener noreferrer" aria-label="查看原图"><img :src="url" :alt="attachment.name" /></a>
    <div class="group-attachment__info"><span><strong>{{ attachment.name }}</strong><small>{{ fileSize(attachment.size) }} · {{ attachment.type || '文件' }}</small></span><button type="button" class="btn btn--ghost btn--sm" :disabled="loading" @click="load(true)">{{ loading ? '加载中' : '下载' }}</button></div>
    <p v-if="error" role="alert">{{ error }} <button type="button" @click="load(lastDownload)">重试</button></p>
  </div>
</template>
<style scoped>
.group-attachment { margin-top:.65rem; max-width:28rem; min-width:0; border:1px solid var(--color-separator); border-radius:12px; overflow:hidden; }.group-attachment img { display:block; max-width:100%; max-height:360px; object-fit:contain; }.group-attachment__preview { display:flex; align-items:center; justify-content:center; gap:.75rem; width:100%; min-height:110px; background:var(--color-fill); color:var(--color-label-secondary); }.group-attachment__info { display:flex; align-items:center; gap:.75rem; padding:.65rem .8rem; }.group-attachment__info>span { flex:1; min-width:0; }.group-attachment strong,.group-attachment small { display:block; overflow-wrap:anywhere; }.group-attachment strong { font-size:.85rem; }.group-attachment small { font-size:.75rem; color:var(--color-label-secondary); }.group-attachment p { padding:.7rem; font-size:.8rem; color:var(--color-danger); }
</style>
