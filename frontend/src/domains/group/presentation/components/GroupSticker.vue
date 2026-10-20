<script setup lang="ts">
/**
 * Inline sticker (Matrix m.sticker or a bridged Telegram sticker): webp as an
 * image, webm as a muted looping video, tgs (gzipped Lottie) via the light
 * lottie-web player (no expressions/eval), loaded on demand. No download card,
 * file name or size; the alt text (often an emoji description) is the title.
 * Animation pauses when the user prefers reduced motion.
 */
import { computed, onBeforeUnmount, onMounted, ref, watch } from 'vue'
import { groupApi } from '../../infrastructure/groupApi'
import type { GroupAttachment } from '../../domain/group'

const props = defineProps<{ attachment: GroupAttachment; alt?: string; hidden?: boolean }>()
const root = ref<HTMLElement>()
const lottieHost = ref<HTMLElement>()
const video = ref<HTMLVideoElement>()
const url = ref(''), kind = ref<'image' | 'video' | 'lottie' | 'fallback' | ''>(''), revealed = ref(false)
const label = computed(() => props.alt?.trim() || '')
const reducedMotion = typeof window !== 'undefined' && typeof window.matchMedia === 'function' ? window.matchMedia('(prefers-reduced-motion: reduce)') : null
const reduced = ref(!!reducedMotion?.matches)
let controller: AbortController | undefined
let observer: IntersectionObserver | undefined
let animation: { destroy(): void; play(): void; pause(): void; goToAndStop(v: number, f?: boolean): void } | undefined
const onMotion = (e: MediaQueryListEvent) => { reduced.value = e.matches; applyMotion() }

/** Sniff the bytes: never trust the declared type of bridged media. */
type StickerFormat = 'webp' | 'png' | 'gif' | 'webm' | 'tgs' | ''
async function sniff(blob: Blob): Promise<StickerFormat> {
  const b = new Uint8Array(await blob.slice(0, 16).arrayBuffer())
  if (b[0] === 0x52 && b[1] === 0x49 && b[2] === 0x46 && b[3] === 0x46 && b[8] === 0x57 && b[9] === 0x45 && b[10] === 0x42 && b[11] === 0x50) return 'webp'
  if (b[0] === 0x89 && b[1] === 0x50 && b[2] === 0x4e && b[3] === 0x47) return 'png'
  if (b[0] === 0x47 && b[1] === 0x49 && b[2] === 0x46) return 'gif'
  if (b[0] === 0x1a && b[1] === 0x45 && b[2] === 0xdf && b[3] === 0xa3) return 'webm'
  if (b[0] === 0x1f && b[1] === 0x8b) return 'tgs'
  return ''
}

function cleanup() {
  controller?.abort(); controller = undefined
  animation?.destroy(); animation = undefined
  if (url.value) URL.revokeObjectURL(url.value)
  url.value = ''; kind.value = ''
}

function applyMotion() {
  if (video.value) { if (reduced.value) video.value.pause(); else { const p = video.value.play?.(); if (p && typeof p.catch === "function") p.catch(() => {}) } }
  if (animation) { if (reduced.value) animation.goToAndStop(0, true); else animation.play() }
}

async function load() {
  if (kind.value || controller) return
  const request = new AbortController(); controller = request
  try {
    const blob = await groupApi.media(props.attachment.url, request.signal)
    if (request.signal.aborted) return
    const format = await sniff(blob)
    if (format === 'webp' || format === 'png' || format === 'gif') {
      url.value = URL.createObjectURL(new Blob([blob], { type: `image/${format}` })); kind.value = 'image'
    } else if (format === 'webm') {
      url.value = URL.createObjectURL(new Blob([blob], { type: 'video/webm' })); kind.value = 'video'
    } else if (format === 'tgs' && typeof DecompressionStream !== 'undefined') {
      const text = await new Response(blob.stream().pipeThrough(new DecompressionStream('gzip'))).text()
      const data = JSON.parse(text) as unknown
      if (request.signal.aborted) return
      kind.value = 'lottie'
      const { default: lottie } = await import('lottie-web/build/player/lottie_light')
      if (request.signal.aborted || !lottieHost.value) return
      animation = lottie.loadAnimation({ container: lottieHost.value, renderer: 'svg', loop: true, autoplay: !reduced.value, animationData: data })
      applyMotion()
    } else kind.value = 'fallback'
  } catch {
    if (!request.signal.aborted) kind.value = 'fallback'
  }
}

watch(() => props.attachment.url, () => { cleanup(); if (!props.hidden || revealed.value) void load() })
watch(video, () => applyMotion())
onMounted(() => {
  reducedMotion?.addEventListener?.('change', onMotion)
  if (typeof IntersectionObserver === 'undefined') { if (!props.hidden) void load(); return }
  observer = new IntersectionObserver(entries => {
    if (entries.some(e => e.isIntersecting)) { observer?.disconnect(); if (!props.hidden) void load() }
  }, { rootMargin: '240px' })
  if (root.value) observer.observe(root.value)
})
onBeforeUnmount(() => { observer?.disconnect(); reducedMotion?.removeEventListener?.('change', onMotion); cleanup() })
function reveal() { revealed.value = true; void load() }
</script>
<template>
  <div ref="root" class="group-sticker" :title="label || undefined" role="img" :aria-label="label || $t('group.GroupMessageView.sticker')" :data-sticker-kind="kind || 'pending'">
    <button v-if="hidden && !revealed" type="button" class="group-sticker__hidden" @click="reveal"><t-icon name="browse-off" :size="22" /></button>
    <img v-else-if="kind === 'image'" :src="url" alt="" draggable="false" />
    <video v-else-if="kind === 'video'" ref="video" :src="url" :autoplay="!reduced" loop muted playsinline disablepictureinpicture preload="auto" aria-hidden="true" />
    <div v-else-if="kind === 'lottie'" ref="lottieHost" class="group-sticker__lottie" aria-hidden="true" />
    <span v-else-if="kind === 'fallback'" class="group-sticker__fallback">{{ label || $t('group.GroupMessageView.sticker') }}</span>
    <span v-else class="group-sticker__pending" aria-hidden="true" />
  </div>
</template>
<style scoped>
.group-sticker { width:144px; height:144px; margin-top:.35rem; display:flex; align-items:center; justify-content:center; }
.group-sticker img,.group-sticker video,.group-sticker__lottie { width:100%; height:100%; object-fit:contain; display:block; background:transparent; }
.group-sticker__pending { width:100%; height:100%; border-radius:12px; background:var(--color-fill); }
.group-sticker__fallback { font-size:.85rem; color:var(--color-label-secondary); text-align:center; padding:.5rem; border-radius:12px; background:var(--color-fill); overflow-wrap:anywhere; }
.group-sticker__hidden { width:100%; height:100%; border-radius:12px; background:var(--color-fill); color:var(--color-label-secondary); display:flex; align-items:center; justify-content:center; }
@media (max-width:480px) { .group-sticker { width:128px; height:128px; } }
</style>
