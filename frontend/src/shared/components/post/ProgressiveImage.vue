<script setup lang="ts">
/**
 * An image that keeps its BlurHash (or an already-loaded low-res stand-in)
 * on screen with a spinner until the real file has loaded, then fades it in.
 * A failed load shows a broken-image mark over the placeholder instead of an
 * endless spinner. Images the browser already has (img.complete on mount)
 * skip the overlay entirely, so cached media never flashes.
 */
import { onMounted, ref, watch } from 'vue'
import BlurPlaceholder from './BlurPlaceholder.vue'

export type ProgressiveState = 'loading' | 'loaded' | 'error'

const props = withDefaults(defineProps<{
  src: string
  alt?: string
  blurhash?: string | null
  /** Low-res image already on screen (e.g. the thumbnail), shown blurred and upscaled while src loads. */
  placeholderSrc?: string | null
  fit?: 'cover' | 'contain'
  lazy?: boolean
  /** Offer a retry button in the error state (not inside another button). */
  retryable?: boolean
  referrerpolicy?: 'no-referrer' | 'origin' | 'same-origin' | 'strict-origin-when-cross-origin'
}>(), { alt: '', blurhash: null, placeholderSrc: null, fit: 'cover', lazy: true, retryable: false, referrerpolicy: undefined })

const emit = defineEmits<{ load: [Event]; error: [Event] }>()

const img = ref<HTMLImageElement>()
const state = ref<ProgressiveState>('loading')
/** True when the image was ready on mount: no overlay, no fade. */
const instant = ref(false)
const attempt = ref(0)

function checkComplete() {
  const el = img.value
  if (el && el.complete && el.naturalWidth > 0) {
    state.value = 'loaded'
    instant.value = true
  }
}

onMounted(checkComplete)
watch(() => props.src, () => {
  state.value = 'loading'
  instant.value = false
  // The new src may already be cached; look once the DOM has it.
  queueMicrotask(checkComplete)
})

function onLoad(event: Event) {
  state.value = 'loaded'
  emit('load', event)
}
function onError(event: Event) {
  state.value = 'error'
  emit('error', event)
}
function retry() {
  state.value = 'loading'
  instant.value = false
  attempt.value++
}

defineExpose({ state, retry })
</script>

<template>
  <div
    class="pimg"
    :class="[`pimg--${state}`, `pimg--${fit}`, { 'pimg--instant': instant }]"
    :data-state="state"
    :aria-busy="state === 'loading' ? 'true' : undefined"
  >
    <template v-if="state !== 'loaded' || !instant">
      <img v-if="placeholderSrc" class="pimg__stand-in" :src="placeholderSrc" alt="" aria-hidden="true" />
      <BlurPlaceholder v-else class="pimg__blur" :blurhash="blurhash" />
    </template>
    <img
      :key="attempt"
      ref="img"
      class="pimg__img"
      :src="src"
      :alt="alt"
      :loading="lazy ? 'lazy' : undefined"
      decoding="async"
      :referrerpolicy="referrerpolicy"
      @load="onLoad"
      @error="onError"
    />
    <span v-if="state === 'loading'" class="pimg__spinner" role="status" aria-label="图片加载中" />
    <span v-else-if="state === 'error'" class="pimg__error">
      <svg width="28" height="28" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" aria-hidden="true">
        <rect x="3" y="3" width="18" height="18" rx="2" />
        <circle cx="8.5" cy="8.5" r="1.5" />
        <path d="M21 15l-5-5L5 21" />
        <path d="M3 3l18 18" />
      </svg>
      <button v-if="retryable" type="button" class="pimg__retry" @click.stop="retry">重试</button>
      <span v-else class="pimg__sr">图片加载失败</span>
    </span>
  </div>
</template>

<style scoped>
.pimg {
  position: relative;
  width: 100%;
  height: 100%;
  overflow: hidden;
}

.pimg__blur,
.pimg__stand-in {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  min-height: 0;
}

.pimg__stand-in {
  object-fit: inherit;
  filter: blur(12px);
  transform: scale(1.04);
}

.pimg__img {
  position: relative;
  display: block;
  width: 100%;
  height: 100%;
  opacity: 0;
  transition: opacity 180ms ease-out;
}

.pimg--cover .pimg__img,
.pimg--cover .pimg__stand-in { object-fit: cover; }
.pimg--contain .pimg__img,
.pimg--contain .pimg__stand-in { object-fit: contain; }

.pimg--loaded .pimg__img { opacity: 1; }
.pimg--instant .pimg__img { transition: none; }
.pimg--error .pimg__img { visibility: hidden; }

.pimg__spinner {
  position: absolute;
  top: 50%;
  left: 50%;
  width: 28px;
  height: 28px;
  margin: -14px 0 0 -14px;
  border: 3px solid rgb(255 255 255 / 35%);
  border-top-color: #fff;
  border-radius: 50%;
  filter: drop-shadow(0 1px 3px rgb(0 0 0 / 35%));
  animation: pimg-spin 0.8s linear infinite;
  pointer-events: none;
}

.pimg__error {
  position: absolute;
  inset: 0;
  display: flex;
  flex-direction: column;
  align-items: center;
  justify-content: center;
  gap: 8px;
  color: #fff;
  background: rgb(0 0 0 / 25%);
  filter: drop-shadow(0 1px 3px rgb(0 0 0 / 40%));
}

.pimg__retry {
  min-height: 2.25rem;
  padding: 0 14px;
  border-radius: 999px;
  background: rgb(0 0 0 / 55%);
  color: #fff;
  font-size: 0.875rem;
}

.pimg__sr {
  position: absolute;
  width: 1px;
  height: 1px;
  overflow: hidden;
  clip-path: inset(50%);
}

@keyframes pimg-spin { to { transform: rotate(360deg); } }

@media (prefers-reduced-motion: reduce) {
  .pimg__img { transition: none; }
  .pimg__spinner { animation: none; border-top-color: rgb(255 255 255 / 35%); border-color: #fff; opacity: 0.8; }
}
</style>
