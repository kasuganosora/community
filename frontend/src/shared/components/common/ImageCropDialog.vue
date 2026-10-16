<script setup lang="ts">
import { computed, nextTick, onBeforeUnmount, onMounted, ref, useId } from 'vue'
import { clampCropCenter, imageCropRect, imageMayAnimate, MAX_CROP_ZOOM, PROFILE_HEADER_RATIO } from '@/shared/utils/imageCrop'
import { validateImageFile } from '@/shared/utils/imageFile'

import { t } from '@/shared/i18n'
const props = defineProps<{ file: File; kind: 'avatar' | 'header' }>()
const emit = defineEmits<{ apply: [file: File]; cancel: [] }>()
const id = useId()
const dialog = ref<HTMLDialogElement>()
const viewport = ref<HTMLElement>()
const loading = ref(true)
const exporting = ref(false)
const error = ref('')
const animated = ref(false)
const imageSize = ref({ width: 1, height: 1 })
const center = ref({ x: .5, y: .5 })
const zoom = ref(1)
const viewportWidth = ref(1)
const aspect = computed(() => props.kind === 'avatar' ? 1 : PROFILE_HEADER_RATIO)
const crop = computed(() => imageCropRect(imageSize.value, aspect.value, zoom.value, center.value))
const imageStyle = computed(() => {
  const scale = viewportWidth.value / crop.value.width
  return {
    width: `${imageSize.value.width * scale}px`, height: `${imageSize.value.height * scale}px`,
    transform: `translate3d(${-crop.value.x * scale}px, ${-crop.value.y * scale}px, 0)`,
  }
})
let sourceUrl = ''
const imageUrl = ref('')
let decodedImage: HTMLImageElement | null = null
let observer: ResizeObserver | undefined
let alive = true
let previousFocus: HTMLElement | null = null
let pointer: { id: number; x: number; y: number } | null = null

function measure() { viewportWidth.value = viewport.value?.getBoundingClientRect().width || 1 }
function setZoom(value: number) {
  zoom.value = Math.max(1, Math.min(MAX_CROP_ZOOM, value))
  center.value = clampCropCenter(imageSize.value, aspect.value, zoom.value, center.value)
}
function move(dx: number, dy: number) {
  const scale = viewportWidth.value / crop.value.width
  center.value = clampCropCenter(imageSize.value, aspect.value, zoom.value, {
    x: center.value.x - dx / scale / imageSize.value.width,
    y: center.value.y - dy / scale / imageSize.value.height,
  })
}
function startDrag(event: PointerEvent) {
  if (pointer || event.button !== 0 || loading.value || exporting.value) return
  pointer = { id: event.pointerId, x: event.clientX, y: event.clientY }
  viewport.value?.setPointerCapture(event.pointerId)
  viewport.value?.focus({ preventScroll: true })
}
function drag(event: PointerEvent) {
  if (pointer?.id !== event.pointerId) return
  move(event.clientX - pointer.x, event.clientY - pointer.y)
  pointer.x = event.clientX; pointer.y = event.clientY
}
function endDrag(event: PointerEvent) {
  if (pointer?.id !== event.pointerId) return
  if (viewport.value?.hasPointerCapture(event.pointerId)) viewport.value.releasePointerCapture(event.pointerId)
  pointer = null
}
function keyMove(event: KeyboardEvent) {
  if (loading.value || exporting.value) return
  const step = event.shiftKey ? 32 : 8
  const directions: Record<string, [number, number]> = { ArrowLeft: [-step, 0], ArrowRight: [step, 0], ArrowUp: [0, -step], ArrowDown: [0, step] }
  if (directions[event.key]) { event.preventDefault(); move(...directions[event.key]) }
  if (['+', '=', '-'].includes(event.key)) { event.preventDefault(); setZoom(zoom.value + (event.key === '-' ? -.1 : .1)) }
}
function cancel() { emit('cancel') }

async function apply() {
  if (!decodedImage || loading.value || exporting.value || error.value) return
  exporting.value = true
  try {
    const canvas = document.createElement('canvas')
    // Do not upscale a small source. PNG preserves transparency in avatars and covers.
    canvas.width = Math.max(1, Math.min(props.kind === 'avatar' ? 512 : 1500, Math.floor(crop.value.width)))
    canvas.height = Math.max(1, Math.round(canvas.width / aspect.value))
    const context = canvas.getContext('2d')
    if (!context) throw new Error(t('shared.ImageCropDialog.s17'))
    context.imageSmoothingQuality = 'high'
    context.drawImage(decodedImage, crop.value.x, crop.value.y, crop.value.width, crop.value.height, 0, 0, canvas.width, canvas.height)
    const blob = await new Promise<Blob>((resolve, reject) => canvas.toBlob(value => value ? resolve(value) : reject(new Error(t('shared.ImageCropDialog.s18'))), 'image/png'))
    if (!alive) return
    const file = new File([blob], `${props.file.name.replace(/\.[^.]+$/, '') || props.kind}-crop.png`, { type: 'image/png' })
    const invalid = validateImageFile(file)
    if (invalid) throw new Error(invalid)
    emit('apply', file)
  } catch (reason) {
    if (alive) error.value = reason instanceof Error ? reason.message : t('shared.ImageCropDialog.s18')
  } finally { if (alive) exporting.value = false }
}

onMounted(async () => {
  previousFocus = document.activeElement instanceof HTMLElement ? document.activeElement : null
  dialog.value?.showModal()
  try {
    const invalid = validateImageFile(props.file)
    if (invalid) throw new Error(invalid)
    if (!/^image\/(jpeg|png|webp|gif|avif|bmp)$/.test(props.file.type)) throw new Error(t('shared.ImageCropDialog.s19'))
    animated.value = imageMayAnimate(props.file.type, new Uint8Array(await props.file.arrayBuffer()))
    if (!alive) return
    sourceUrl = URL.createObjectURL(props.file)
    const image = new Image()
    // Browser image decoding honors EXIF orientation; preview and canvas share this image.
    await new Promise<void>((resolve, reject) => {
      image.onload = () => resolve()
      image.onerror = () => reject(new Error(t('shared.ImageCropDialog.s20')))
      image.src = sourceUrl
    })
    if (!alive) return
    if (!image.naturalWidth || !image.naturalHeight || image.naturalWidth * image.naturalHeight > 40_000_000
      || Math.max(image.naturalWidth, image.naturalHeight) > 16384) throw new Error(t('shared.ImageCropDialog.s21'))
    decodedImage = image
    imageSize.value = { width: image.naturalWidth, height: image.naturalHeight }
    imageUrl.value = sourceUrl
    loading.value = false
    await nextTick()
    if (!alive) return
    measure()
    observer = new ResizeObserver(measure)
    if (viewport.value) observer.observe(viewport.value)
  } catch (reason) {
    if (alive) { loading.value = false; error.value = reason instanceof Error ? reason.message : t('shared.ImageCropDialog.s22') }
  }
})

onBeforeUnmount(() => {
  alive = false
  observer?.disconnect()
  decodedImage = null
  if (sourceUrl) URL.revokeObjectURL(sourceUrl)
  dialog.value?.close()
  if (previousFocus?.isConnected) previousFocus.focus({ preventScroll: true })
})
</script>

<template>
  <Teleport to="body">
    <dialog ref="dialog" class="image-crop" :aria-labelledby="`${id}-title`" @cancel.prevent="cancel">
      <header class="image-crop__header">
        <button type="button" class="btn btn--ghost image-crop__cancel" autofocus :aria-label="$t('shared.ImageCropDialog.s1')" @click="cancel">{{ $t('shared.ImageCropDialog.s2') }}</button>
        <h2 :id="`${id}-title`">{{ kind === 'avatar' ? $t('shared.ImageCropDialog.s3') : $t('shared.ImageCropDialog.s4') }}</h2>
        <button type="button" class="btn btn--primary" :disabled="loading || exporting || !!error" @click="apply">{{ exporting ? $t('shared.ImageCropDialog.s5') : $t('shared.ImageCropDialog.s6') }}</button>
      </header>
      <div class="image-crop__body">
        <p v-if="loading" class="image-crop__status" role="status">{{ $t('shared.ImageCropDialog.s7') }}</p>
        <p v-else-if="error" class="image-crop__error" role="alert">{{ error }}</p>
        <template v-else>
          <div ref="viewport" class="image-crop__viewport" :class="{ 'image-crop__viewport--avatar': kind === 'avatar' }" :style="{ aspectRatio: aspect }"
            role="group" tabindex="0" :aria-label="$t('shared.ImageCropDialog.s8')" :aria-describedby="`${id}-hint`"
            @pointerdown="startDrag" @pointermove="drag" @pointerup="endDrag" @pointercancel="endDrag" @lostpointercapture="pointer = null" @keydown="keyMove"
          >
            <img :src="imageUrl" :style="imageStyle" alt="" draggable="false" />
            <div class="image-crop__guide" aria-hidden="true" />
          </div>
          <p :id="`${id}-hint`" class="image-crop__hint">{{ $t('shared.ImageCropDialog.s11', [kind === 'avatar' ? $t('shared.ImageCropDialog.s9') : $t('shared.ImageCropDialog.s10')]) }}</p>
          <div class="image-crop__zoom">
            <button type="button" class="btn btn--ghost" :aria-label="$t('shared.ImageCropDialog.s12')" :disabled="zoom <= 1 || exporting" @click="setZoom(zoom - .1)">−</button>
            <label class="sr-only" :for="`${id}-zoom`">{{ $t('shared.ImageCropDialog.s13') }}</label>
            <input :id="`${id}-zoom`" type="range" min="1" :max="MAX_CROP_ZOOM" step="0.01" :value="zoom" :disabled="exporting" :aria-valuetext="`${Math.round(zoom * 100)}%`" @input="setZoom(Number(($event.target as HTMLInputElement).value))" />
            <button type="button" class="btn btn--ghost" :aria-label="$t('shared.ImageCropDialog.s14')" :disabled="zoom >= MAX_CROP_ZOOM || exporting" @click="setZoom(zoom + .1)">+</button>
            <output>{{ Math.round(zoom * 100) }}%</output>
          </div>
          <div v-if="animated" class="image-crop__animation" role="note">
            <p>{{ $t('shared.ImageCropDialog.s15') }}</p>
            <button type="button" class="btn btn--secondary" :disabled="exporting" @click="emit('apply', file)">{{ $t('shared.ImageCropDialog.s16') }}</button>
          </div>
        </template>
      </div>
    </dialog>
  </Teleport>
</template>

<style scoped>
.image-crop { width: min(760px, calc(100vw - 32px)); max-width: none; max-height: calc(100dvh - 32px); margin: auto; padding: 0; border: 1px solid var(--color-separator); border-radius: 20px; background: var(--color-bg-elevated, var(--bg-primary)); color: var(--text-primary); box-shadow: 0 24px 80px rgb(0 0 0 / 35%); overflow: auto; }
.image-crop::backdrop { background: rgb(0 0 0 / 65%); }
.image-crop__header { position: sticky; top: 0; z-index: 1; display: flex; align-items: center; gap: 12px; padding: 12px 16px; background: var(--color-bg-elevated, var(--bg-primary)); border-bottom: 1px solid var(--color-separator); }
.image-crop__header h2 { flex: 1; margin: 0; font-size: 18px; font-weight: 650; }
.image-crop .btn { min-height: 44px; padding: 8px 16px; white-space: nowrap; }
.image-crop .btn:active:not(:disabled) { transform: scale(.97); }
.image-crop__body { padding: 24px; }
.image-crop__viewport { position: relative; width: 100%; margin: 0 auto; overflow: hidden; cursor: grab; touch-action: none; user-select: none; background: repeating-conic-gradient(#c6c6c6 0% 25%, #ededed 0% 50%) 50% / 20px 20px; outline: 1px solid var(--color-separator); }
.image-crop__viewport--avatar { width: min(100%, 48dvh, 480px); }
.image-crop__viewport:active { cursor: grabbing; }
.image-crop__viewport:focus-visible { outline: 3px solid var(--color-accent, var(--accent-blue)); outline-offset: 4px; }
.image-crop__viewport img { display: block; position: absolute; top: 0; left: 0; max-width: none; pointer-events: none; image-orientation: from-image; }
.image-crop__guide { position: absolute; inset: 0; border: 2px solid rgb(255 255 255 / 85%); pointer-events: none; }
.image-crop__viewport--avatar .image-crop__guide { border-radius: 50%; box-shadow: 0 0 0 100vmax rgb(0 0 0 / 45%); }
.image-crop__hint { margin: 16px 0 8px; color: var(--text-secondary); font-size: 13px; line-height: 1.6; text-align: center; }
.image-crop__zoom { display: flex; align-items: center; gap: 8px; max-width: 540px; margin: 0 auto; }
.image-crop__zoom input { flex: 1; min-width: 0; height: 44px; accent-color: var(--color-accent, var(--accent-blue)); }
.image-crop__zoom output { width: 3.5em; text-align: right; font-size: 13px; color: var(--text-secondary); font-variant-numeric: tabular-nums; }
.image-crop__zoom .btn { width: 44px; padding: 0; font-size: 24px; }
.image-crop__animation { display: flex; align-items: center; gap: 16px; padding-top: 12px; border-top: 1px solid var(--color-separator); margin-top: 16px; }
.image-crop__animation p { flex: 1; margin: 0; color: var(--text-secondary); font-size: 13px; line-height: 1.6; }
.image-crop__status, .image-crop__error { padding: 24px 0; margin: 0; }
.image-crop__error { color: var(--color-danger, #e0245e); }
@media (max-width: 480px) {
  .image-crop { width: calc(100vw - 16px); max-height: calc(100dvh - 16px); border-radius: 16px; }
  .image-crop__header { padding: 10px 12px; gap: 8px; }
  .image-crop__body { padding: 16px; }
  .image-crop__animation { align-items: stretch; flex-direction: column; gap: 12px; }
}
@media (prefers-reduced-motion: reduce) { .image-crop .btn:active:not(:disabled) { transform: none; } }
</style>
