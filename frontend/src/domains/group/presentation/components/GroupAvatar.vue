<script setup lang="ts">
import { computed, ref, watch, onBeforeUnmount } from 'vue'
import { safeGroupURL } from '../../domain/group'
import { groupApi } from '../../infrastructure/groupApi'
const props = withDefaults(defineProps<{ name: string; url?: string; size?: number }>(), { size: 42 })
const source = ref('')
const failed = ref(false)
let objectURL = '', controller: AbortController | undefined
const hue = computed(() => [...props.name].reduce((hash, char) => ((hash * 31) + char.codePointAt(0)!) | 0, 0) % 360)
watch(() => props.url, async raw => {
  controller?.abort(); if (objectURL) URL.revokeObjectURL(objectURL)
  source.value = ''; failed.value = false; objectURL = ''; controller = new AbortController()
  const signal = controller.signal, url = safeGroupURL(raw)
  if (!url) return
  const parsed = new URL(url)
  if (parsed.origin === window.location.origin && parsed.pathname.startsWith('/api/v1/chat/groups/')) {
    try { const blob = await groupApi.media(url, signal); if (!signal.aborted && /^(image\/(png|jpeg|gif|webp))$/.test(blob.type)) { objectURL = URL.createObjectURL(blob); source.value = objectURL } } catch { /* algorithmic fallback */ }
  } else source.value = url
}, { immediate: true })
onBeforeUnmount(() => { controller?.abort(); if (objectURL) URL.revokeObjectURL(objectURL) })
</script>
<template><span class="group-avatar" :style="{ width: `${size}px`, height: `${size}px`, '--avatar-hue': hue }" aria-hidden="true"><img v-if="source && !failed" :src="source" alt="" referrerpolicy="no-referrer" @error="failed = true" /><span v-else>{{ [...name.trim()].slice(0, 1).join('') || '#' }}</span></span></template>
<style scoped>
.group-avatar { display:inline-grid; place-items:center; flex:none; overflow:hidden; border-radius:30%; background:hsl(var(--avatar-hue) 48% 90%); color:hsl(var(--avatar-hue) 55% 28%); font-size:1.1rem; font-weight:650; }.group-avatar img { width:100%; height:100%; object-fit:cover; }
</style>
