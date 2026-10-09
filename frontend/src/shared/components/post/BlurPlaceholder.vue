<script setup lang="ts">
/**
 * Stand-in for hidden media: the attachment's BlurHash painted on a tiny
 * canvas, or a neutral gradient when there is none or it is invalid. It never
 * requests anything from the network.
 */
import { onMounted, ref, watch } from 'vue'
import { decodeBlurhash, isValidBlurhash } from '@/shared/utils/blurhash'

const props = defineProps<{
  blurhash?: string | null
  /** width / height of the real media */
  aspect?: number | null
  /** Show a play mark (video / gifv). */
  play?: boolean
}>()

const SIZE = 32
const canvas = ref<HTMLCanvasElement>()
const painted = ref(false)

function paint() {
  painted.value = false
  const el = canvas.value
  if (!el || !isValidBlurhash(props.blurhash)) return
  try {
    const ctx = el.getContext('2d')
    if (!ctx) return
    const pixels = decodeBlurhash(props.blurhash, SIZE, SIZE)
    const image = ctx.createImageData(SIZE, SIZE)
    image.data.set(pixels)
    ctx.putImageData(image, 0, 0)
    painted.value = true
  } catch {
    painted.value = false
  }
}

onMounted(paint)
watch(() => props.blurhash, paint)
</script>

<template>
  <div
    class="blur-ph"
    :class="{ 'blur-ph--plain': !painted }"
    :style="aspect && aspect > 0 ? { aspectRatio: String(aspect) } : undefined"
    :data-blurhash="isValidBlurhash(blurhash) ? blurhash : undefined"
    aria-hidden="true"
  >
    <canvas v-if="isValidBlurhash(blurhash)" ref="canvas" class="blur-ph__canvas" :width="SIZE" :height="SIZE" />
    <span v-if="play" class="blur-ph__play">▶</span>
  </div>
</template>

<style scoped>
.blur-ph {
  position: relative;
  width: 100%;
  height: 100%;
  min-height: 4rem;
  overflow: hidden;
  background: linear-gradient(135deg, var(--color-fill) 0%, var(--color-fill-strong) 100%);
}

.blur-ph__canvas {
  position: absolute;
  inset: 0;
  width: 100%;
  height: 100%;
  display: block;
}

.blur-ph__play {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  color: #fff;
  font-size: 2rem;
  text-shadow: 0 1px 6px rgb(0 0 0 / 50%);
}
</style>
