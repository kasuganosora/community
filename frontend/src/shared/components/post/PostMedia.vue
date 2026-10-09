<script setup lang="ts">
import { ref, computed } from 'vue'
import type { MastodonAttachment } from '@/shared/types/mastodon'
import { sameOriginMediaUrl, isProxiedMediaUrl } from '@/shared/utils/mediaUrl'
import BlurPlaceholder from './BlurPlaceholder.vue'
import { attachmentAspect } from '@/shared/composables/useMediaMode'
import { postApi } from '@/domains/post/infrastructure/postApi'

const props = defineProps<{
  attachments: MastodonAttachment[]
  /** Status the media belongs to; lets expired proxy links be renewed. */
  statusId?: string
  /** Show BlurHash placeholders only; the real files are not requested. */
  hidden?: boolean
}>()

// Proxied links stop working after about an hour. A page that stays open
// longer asks the server for the status once and swaps in fresh links.
const renewed = ref<MastodonAttachment[] | null>(null)
const items = computed(() => renewed.value ?? props.attachments)
let renewTried = false

const failedMedia = ref<Set<string>>(new Set())
const viewer = ref<number | null>(null)

function markFailed(id: string) {
  const newSet = new Set(failedMedia.value)
  newSet.add(id)
  failedMedia.value = newSet
}

async function linkExpired(url: string): Promise<boolean> {
  try {
    const res = await fetch(url, { method: 'HEAD' })
    return res.status === 410 || res.status === 403
  } catch {
    return false
  }
}

async function handleMediaError(id: string) {
  const attachment = items.value.find(a => a.id === id)
  const url = attachment ? attachment.preview_url || attachment.url : ''
  if (!props.statusId || renewTried || !attachment || !isProxiedMediaUrl(url) || !(await linkExpired(sameOriginMediaUrl(url)))) {
    markFailed(id)
    return
  }
  renewTried = true
  try {
    const status = await postApi.getStatus(props.statusId)
    if (!status.media_attachments?.length) throw new Error('no media')
    renewed.value = status.media_attachments
    failedMedia.value = new Set()
  } catch {
    markFailed(id)
  }
}

function openViewer(index: number) {
  viewer.value = index
}

function moveViewer(step: number) {
  if (viewer.value === null) return
  const count = images.value.length
  viewer.value = (viewer.value + step + count) % count
}

function onViewerKey(event: KeyboardEvent) {
  if (event.key === 'Escape') viewer.value = null
  else if (event.key === 'ArrowRight') moveViewer(1)
  else if (event.key === 'ArrowLeft') moveViewer(-1)
}

const images = computed(() =>
  items.value.filter(a => a.type === 'image')
)

const videos = computed(() =>
  items.value.filter(a => a.type === 'video' || a.type === 'gifv')
)

const audios = computed(() =>
  items.value.filter(a => a.type === 'audio')
)

// Inline media (first four images, videos, audio) is already shown; only
// files without a preview, images past the fourth and media that failed to
// load get a plain link to the original.
const shownInline = computed(() => {
  const ids = new Set<string>()
  images.value.slice(0, 4).forEach(a => ids.add(a.id))
  videos.value.forEach(a => ids.add(a.id))
  audios.value.forEach(a => ids.add(a.id))
  failedMedia.value.forEach(id => ids.delete(id))
  return ids
})

// The fallback link opens the original file: remote_url when the API proxies
// the media, so a broken proxy never links back to itself.
const fileLinks = computed(() => items.value.flatMap((attachment, index) => {
  if (props.hidden) return []
  if (shownInline.value.has(attachment.id)) return []
  try {
    const target = attachment.remote_url || attachment.url
    const url = new URL(target, window.location.origin)
    if (!target || !['http:', 'https:'].includes(url.protocol) || url.username || url.password) return []
    let filename = url.pathname.split('/').pop() || '文件'
    try { filename = decodeURIComponent(filename) } catch { /* Keep the original filename if malformed. */ }
    return [{ id: attachment.id, url: sameOriginMediaUrl(target) || url.href, label: `打开附件 ${index + 1}：${attachment.description || filename}` }]
  } catch { return [] }
}))
const hasMedia = computed(() => items.value.length > 0)

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
    <ul v-if="fileLinks.length" class="media-file-links" aria-label="原始附件">
      <li v-for="file in fileLinks" :key="file.id"><a :href="file.url" target="_blank" rel="noopener noreferrer" @click.stop>{{ file.label }}</a></li>
    </ul>
    <!-- Images -->
    <div :class="gridClass" v-if="images.length && hidden">
      <div v-for="img in images.slice(0, 4)" :key="img.id" class="media-item media-item--hidden">
        <BlurPlaceholder :blurhash="img.blurhash" :aspect="images.length === 1 ? attachmentAspect(img) || 16 / 9 : null" />
      </div>
    </div>
    <div :class="gridClass" v-else-if="images.length">
      <button
        v-for="(img, index) in images.slice(0, 4)"
        :key="img.id"
        type="button"
        class="media-item"
        :aria-label="img.description ? `查看图片：${img.description}` : `查看第 ${index + 1} 张图片`"
        @click.stop="openViewer(index)"
      >
        <img
          v-if="!failedMedia.has(img.id)"
          :src="sameOriginMediaUrl(img.preview_url || img.url)"
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
      </button>
    </div>

    <div
      v-if="!hidden && viewer !== null && images[viewer]"
      class="media-viewer"
      role="dialog"
      aria-modal="true"
      aria-label="图片预览"
      tabindex="-1"
      @click.stop
      @keydown="onViewerKey"
    >
      <div class="media-viewer__bar">
        <span>{{ viewer + 1 }} / {{ images.length }}</span>
        <button type="button" @click="viewer = null">关闭</button>
      </div>
      <img
        class="media-viewer__img"
        :src="sameOriginMediaUrl(images[viewer].url)"
        :alt="images[viewer].description || ''"
      />
      <p v-if="images[viewer].description" class="media-viewer__alt">{{ images[viewer].description }}</p>
      <div v-if="images.length > 1" class="media-viewer__nav">
        <button type="button" @click="moveViewer(-1)">上一张</button>
        <button type="button" @click="moveViewer(1)">下一张</button>
      </div>
    </div>

    <!-- Videos / GIFs -->
    <div v-for="vid in videos" :key="vid.id" class="media-video-wrap">
      <BlurPlaceholder v-if="hidden" :blurhash="vid.blurhash" :aspect="attachmentAspect(vid) || 16 / 9" play />
      <video
        v-else-if="!failedMedia.has(vid.id)"
        :src="sameOriginMediaUrl(vid.url)"
        :poster="sameOriginMediaUrl(vid.preview_url) || undefined"
        controls
        loop
        playsinline
        class="media-video"
        @click.stop
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
      <BlurPlaceholder v-if="hidden" :blurhash="aud.blurhash" :aspect="8" />
      <audio
        v-else-if="!failedMedia.has(aud.id)"
        :src="sameOriginMediaUrl(aud.url)"
        controls
        preload="none"
        class="media-audio"
        @click.stop
        @error="handleMediaError(aud.id)"
      />
      <div v-else class="media-fallback media-fallback--audio">
        <span>音频加载失败</span>
      </div>
    </div>
  </div>
</template>


<style scoped>
.media-file-links { margin: 0 0 var(--space-2); padding-left: var(--space-4); overflow-wrap: anywhere; }
.media-file-links a { color: var(--color-accent-text); text-decoration: underline; }
.post-media {
  margin-top: var(--space-2);
}

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

.media-grid--single .media-item--hidden {
  cursor: default;
}

.media-img {
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
  padding: 0;
  border: 0;
  background: transparent;
  color: inherit;
  text-align: inherit;
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
  min-height: 6rem;
  color: var(--color-label-secondary);
  background: var(--color-fill);
}

.media-overflow {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
  background: rgba(0, 0, 0, 0.45);
  color: #fff;
  font-size: var(--text-title-2);
  font-weight: var(--weight-semibold);
  pointer-events: none;
}

.media-video,
.media-audio {
  display: block;
  width: 100%;
  margin-top: var(--space-2);
  border-radius: var(--radius-lg);
  background: #000;
}

.media-audio {
  background: var(--color-fill);
}

@media (max-width: 43.74rem) {
  [class^='media-grid--'] {
    max-height: 16rem;
    aspect-ratio: 1 / 1;
  }

  .media-grid--single {
    aspect-ratio: auto;
    max-height: none;
  }

  .media-grid--single .media-img {
    max-height: 18rem;
    object-fit: contain;
    background: var(--color-fill);
  }
}

.media-viewer {
  position: fixed;
  inset: 0;
  z-index: 80;
  display: flex;
  flex-direction: column;
  gap: var(--space-3);
  padding: calc(var(--space-4) + var(--safe-top)) var(--gutter) calc(var(--space-4) + var(--safe-bottom));
  background: rgba(0, 0, 0, 0.92);
  color: #fff;
}

.media-viewer__bar,
.media-viewer__nav {
  display: flex;
  align-items: center;
  justify-content: space-between;
  gap: var(--space-3);
}

.media-viewer__bar button,
.media-viewer__nav button {
  min-height: 2.75rem;
  padding: 0 var(--space-4);
  border-radius: var(--radius-full);
  background: rgba(255, 255, 255, 0.16);
  color: #fff;
}

.media-viewer__img {
  flex: 1;
  min-height: 0;
  width: 100%;
  object-fit: contain;
}

.media-viewer__alt {
  margin: 0;
  font-size: var(--text-footnote);
  line-height: var(--leading-footnote);
  overflow-wrap: anywhere;
}
</style>
