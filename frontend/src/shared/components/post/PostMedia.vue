<script setup lang="ts">
import { ref, computed } from 'vue'
import type { MastodonAttachment } from '@/shared/types/mastodon'

const props = defineProps<{
  attachments: MastodonAttachment[]
}>()

const failedMedia = ref<Set<string>>(new Set())

function handleMediaError(id: string) {
  const newSet = new Set(failedMedia.value)
  newSet.add(id)
  failedMedia.value = newSet
}

const images = computed(() =>
  props.attachments.filter(a => a.type === 'image')
)

const videos = computed(() =>
  props.attachments.filter(a => a.type === 'video' || a.type === 'gifv')
)

const audios = computed(() =>
  props.attachments.filter(a => a.type === 'audio')
)

const hasMedia = computed(() =>
  images.value.length > 0 || videos.value.length > 0 || audios.value.length > 0
)

const gridClass = computed(() => {
  const count = images.value.length
  if (count === 1) return 'media-grid--single'
  if (count === 2) return 'media-grid--double'
  if (count === 3) return 'media-grid--triple'
  return 'media-grid--quad'
})

</script>


<template>
  <div class="post-media" v-if="hasMedia">
    <!-- Images -->
    <div :class="gridClass" v-if="images.length">
      <div
        v-for="(img, index) in images.slice(0, 4)"
        :key="img.id"
        class="media-item"
      >
        <img
          v-if="!failedMedia.has(img.id)"
          :src="img.preview_url || img.url"
          :alt="img.description || ''"
          loading="lazy"
          class="media-img"
          @error="handleMediaError(img.id)"
        />
        <div v-else class="media-fallback">
          <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
            <rect x="3" y="3" width="18" height="18" rx="2" />
            <circle cx="8.5" cy="8.5" r="1.5" />
            <path d="M21 15l-5-5L5 21" />
          </svg>
        </div>
        <div v-if="index === 3 && images.length > 4" class="media-overflow">
          +{{ images.length - 4 }}
        </div>
      </div>
    </div>

    <!-- Videos / GIFs -->
    <div v-for="vid in videos" :key="vid.id" class="media-video-wrap">
      <video
        v-if="!failedMedia.has(vid.id)"
        :src="vid.url"
        :poster="vid.preview_url || undefined"
        controls
        loop
        playsinline
        class="media-video"
        @error="handleMediaError(vid.id)"
      />
      <div v-else class="media-fallback media-fallback--video">
        <svg width="32" height="32" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2">
          <polygon points="23 7 16 12 23 17 23 7" />
          <rect x="1" y="5" width="15" height="14" rx="2" />
        </svg>
      </div>
    </div>

    <!-- Audio -->
    <div v-for="aud in audios" :key="aud.id" class="media-audio-wrap">
      <audio
        v-if="!failedMedia.has(aud.id)"
        :src="aud.url"
        controls
        preload="none"
        class="media-audio"
        @error="handleMediaError(aud.id)"
      />
      <div v-else class="media-fallback media-fallback--audio">
        <span>音频加载失败</span>
      </div>
    </div>
  </div>
</template>


<style scoped>
.post-media {
  margin-top: var(--space-2);
}

/* Image grid: continuous rounded frame, hairline gutters, and a hairline
   inner stroke so light photos don't bleed into a light background. */
[class^='media-grid--'] {
  position: relative;
  display: grid;
  gap: 2px;
  border-radius: var(--radius-lg);
  overflow: hidden;
  aspect-ratio: 16 / 9;
  max-height: 32rem;
  background: var(--color-fill);
  isolation: isolate;
}

[class^='media-grid--']::after {
  content: '';
  position: absolute;
  inset: 0;
  border-radius: inherit;
  box-shadow: inset 0 0 0 var(--hairline) var(--color-separator);
  pointer-events: none;
}

.media-grid--single {
  grid-template-columns: 1fr;
  aspect-ratio: auto;
}

.media-grid--single .media-img {
  max-height: 32rem;
  height: auto;
}

.media-grid--double {
  grid-template-columns: 1fr 1fr;
}

.media-grid--triple,
.media-grid--quad {
  grid-template-columns: 1fr 1fr;
  grid-template-rows: 1fr 1fr;
}

.media-grid--triple .media-item:first-child {
  grid-row: span 2;
}

.media-item {
  position: relative;
  overflow: hidden;
  cursor: pointer;
  min-height: 0;
}

.media-img {
  display: block;
  width: 100%;
  height: 100%;
  object-fit: cover;
  transition: filter var(--duration-fast) var(--ease-out);
}

.media-item:active .media-img {
  filter: brightness(0.9);
  transition-duration: 0s;
}

.media-fallback {
  display: flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  min-height: 7.5rem;
  background: var(--color-fill);
  color: var(--color-label-tertiary);
  font-size: var(--text-footnote);
}

.media-fallback--video {
  min-height: 12.5rem;
  background: #000;
  color: #8e8e93;
}

.media-fallback--audio {
  min-height: 3rem;
  border-radius: var(--radius-md);
}

.media-video-wrap {
  margin-top: var(--space-2);
}

.media-video {
  display: block;
  width: 100%;
  max-height: 32rem;
  object-fit: contain;
  border-radius: var(--radius-lg);
  background: #000;
}

.media-audio-wrap {
  margin-top: var(--space-2);
}

.media-audio {
  width: 100%;
}

.media-overflow {
  position: absolute;
  inset: 0;
  display: flex;
  align-items: center;
  justify-content: center;
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
  font-size: var(--text-title-2);
  font-weight: var(--weight-semibold);
  letter-spacing: var(--tracking-title-2);
}
</style>
