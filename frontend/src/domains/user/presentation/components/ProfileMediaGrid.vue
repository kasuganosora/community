<script setup lang="ts">
/** Profile "媒体" tab: a square thumbnail grid that opens the post. */
import { computed, onMounted, onUnmounted, ref, watch } from 'vue'
import { useRouter } from 'vue-router'
import type { MastodonAttachment, MastodonStatus } from '@/shared/types/mastodon'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'
import BlurPlaceholder from '@/shared/components/post/BlurPlaceholder.vue'
import { mediaGated, useMediaMode } from '@/shared/composables/useMediaMode'

const props = defineProps<{
  statuses: MastodonStatus[]
  loading?: boolean
  hasMore?: boolean
  error?: string | null
}>()

const emit = defineEmits<{ loadMore: []; retry: [] }>()
const router = useRouter()
const sentinel = ref<HTMLElement>()
const revealed = ref(new Set<string>())
const { mode: mediaMode } = useMediaMode()

interface Tile { key: string; status: MastodonStatus; media: MastodonAttachment; src: string; videoSrc: string; hidden: boolean }

// Some remote servers send the video file itself as preview_url.
const NOT_AN_IMAGE = /\.(mp4|m4v|webm|mov|ogv|mp3|m4a|ogg|oga|opus|wav|flac)(\?|#|$)/i
const failed = ref(new Set<string>())
function thumbnail(media: MastodonAttachment): string {
  const src = media.preview_url || (media.type === 'image' ? media.url : '')
  return src && !NOT_AN_IMAGE.test(src) ? sameOriginMediaUrl(src) : ''
}
function markFailed(key: string) {
  failed.value = new Set(failed.value).add(key)
}

const tiles = computed<Tile[]>(() => props.statuses.flatMap((item) => {
  const status = item.reblog?.id ? item.reblog : item
  const hidden = (mediaGated(status, mediaMode.value) || (mediaMode.value !== 'show_all' && !!status.spoiler_text)) && !revealed.value.has(status.id)
  return (status.media_attachments ?? []).map((media) => {
    const key = `${item.id}-${media.id}`
    return {
      key,
      status,
      media,
      // Hidden tiles carry no file URL at all, so nothing is requested.
      src: hidden || failed.value.has(key) ? '' : thumbnail(media),
      videoSrc: !hidden && (media.type === 'video' || media.type === 'gifv') ? sameOriginMediaUrl(media.url || '') : '',
      hidden,
    }
  })
}))

function open(tile: Tile) {
  if (tile.hidden) {
    revealed.value = new Set(revealed.value).add(tile.status.id)
    return
  }
  router.push({
    name: 'post-detail',
    params: { username: tile.status.account.acct || tile.status.account.username, statusId: tile.status.id },
  }).catch(() => {})
}

const PREFETCH_PX = 1200
let observer: IntersectionObserver | null = null
function loadIfVisible() {
  if (props.loading || props.error || props.hasMore === false || !props.statuses.length || !sentinel.value) return
  const rect = sentinel.value.getBoundingClientRect()
  if (rect.top <= window.innerHeight + PREFETCH_PX) emit('loadMore')
}
watch([() => props.loading, () => props.statuses.length, () => props.hasMore], loadIfVisible, { flush: 'post' })
onMounted(() => {
  if (typeof IntersectionObserver === 'undefined') return
  observer = new IntersectionObserver(() => loadIfVisible(), { rootMargin: `${PREFETCH_PX}px 0px` })
  if (sentinel.value) observer.observe(sentinel.value)
})
onUnmounted(() => observer?.disconnect())
</script>

<template>
  <div class="media-tab">
    <div v-if="tiles.length" class="media-tab__grid">
      <button
        v-for="tile in tiles"
        :key="tile.key"
        type="button"
        class="media-tab__tile"
        :class="{ 'is-hidden': tile.hidden }"
        :aria-label="tile.hidden ? '敏感内容，点击查看' : (tile.media.description || '查看帖子')"
        @click="open(tile)"
      >
        <BlurPlaceholder v-if="tile.hidden" :blurhash="tile.media.blurhash" :aspect="1" />
        <img v-else-if="tile.src" :src="tile.src" :alt="tile.media.description || ''" loading="lazy" decoding="async" @error="markFailed(tile.key)" />
        <video v-else-if="tile.videoSrc && !failed.has(tile.key + ':video')" :src="`${tile.videoSrc}#t=0.1`" preload="metadata" muted playsinline tabindex="-1" aria-hidden="true" @error="markFailed(tile.key + ':video')" />
        <span v-else class="media-tab__placeholder"><t-icon name="file" :size="24" /></span>
        <span v-if="tile.media.type === 'video' || tile.media.type === 'gifv'" class="media-tab__badge" aria-hidden="true">▶</span>
        <span v-if="tile.hidden" class="media-tab__sensitive">敏感内容</span>
      </button>
    </div>
    <div ref="sentinel" class="media-tab__sentinel" />
    <div v-if="loading" class="media-tab__status"><t-loading /></div>
    <div v-else-if="error" class="media-tab__status" role="alert">
      <span>{{ error }}</span>
      <button type="button" class="btn btn--secondary btn--sm" @click="statuses.length ? emit('loadMore') : emit('retry')">重试</button>
    </div>
    <div v-else-if="!tiles.length" class="empty-state">
      <div class="empty-state__icon"><t-icon name="image" :size="48" /></div>
      <h2 class="empty-state__title">暂无媒体</h2>
    </div>
    <div v-else-if="hasMore" class="media-tab__status">
      <button type="button" class="btn btn--secondary btn--sm" @click="emit('loadMore')">加载更多</button>
    </div>
  </div>
</template>

<style scoped>
.media-tab__grid {
  display: grid;
  grid-template-columns: repeat(3, minmax(0, 1fr));
  gap: 2px;
}

.media-tab__tile {
  position: relative;
  aspect-ratio: 1;
  overflow: hidden;
  background: var(--color-fill);
  padding: 0;
}

.media-tab__tile img,
.media-tab__tile video {
  width: 100%;
  height: 100%;
  object-fit: cover;
  transition: transform var(--duration-release) var(--ease-out);
}

.media-tab__tile > .blur-ph {
  position: absolute;
  inset: 0;
}

.media-tab__placeholder,
.media-tab__sensitive,
.media-tab__badge {
  position: absolute;
  inset: 0;
  display: grid;
  place-items: center;
}

.media-tab__placeholder {
  color: var(--color-label-secondary);
}

.media-tab__badge {
  color: #fff;
  font-size: 1.5rem;
  text-shadow: 0 1px 4px rgb(0 0 0 / 50%);
  pointer-events: none;
}

.media-tab__sensitive {
  background: rgb(0 0 0 / 25%);
  color: #fff;
  font-size: var(--text-footnote);
  font-weight: var(--weight-semibold);
}

.media-tab__sentinel {
  height: 1px;
}

.media-tab__status {
  display: flex;
  align-items: center;
  justify-content: center;
  gap: var(--space-2);
  padding: var(--space-6) var(--gutter);
  color: var(--color-label-secondary);
  font-size: var(--text-footnote);
}
</style>
