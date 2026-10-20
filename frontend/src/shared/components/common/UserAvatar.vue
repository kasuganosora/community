<script setup lang="ts">
import { computed, nextTick, ref, watch } from 'vue'
import type { AvatarDecorationPlacement, MastodonAccount } from '@/shared/types/mastodon'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'
import { avatarIdentity, identiconPattern } from '@/shared/utils/accountAvatar'

const props = defineProps<{
  account: MastodonAccount
  size?: number
  /** Overrides the account's decorations (settings preview). */
  decorations?: AvatarDecorationPlacement[]
  /** Show the Misskey-style online indicator (profile header). */
  showOnline?: boolean
  /** Play an animated avatar (profile header); elsewhere only on hover. */
  animated?: boolean
}>()

// Only this server's decoration endpoint and the media proxy are trusted:
// remote decoration images must never be hotlinked.
const LOCAL_DECORATION = /^\/api\/v1\/avatar_decorations\/\d+\/image$/
const PROXIED_DECORATION = /^\/proxy\/media\/[A-Za-z0-9_-]+$/
function decorationUrl(raw: string | undefined): string {
  if (!raw) return ''
  if (LOCAL_DECORATION.test(raw)) return raw
  const proxied = sameOriginMediaUrl(raw)
  return PROXIED_DECORATION.test(proxied) ? proxied : ''
}
function finite(v: number | undefined, limit: number): number {
  return typeof v === 'number' && Number.isFinite(v) ? Math.max(-limit, Math.min(limit, v)) : 0
}
const decorations = computed(() => {
  const source = props.decorations ?? (props.account.avatar_decorations?.length
    ? props.account.avatar_decorations
    : props.account.avatar_decoration_url ? [{ id: props.account.avatar_decoration_id || 'legacy', url: props.account.avatar_decoration_url }] : [])
  return source.slice(0, 16).map((d, index) => {
    const url = decorationUrl(d.url)
    if (!url) return null
    // Same CSS individual transform properties as Misskey's MkAvatar.vue:
    // translate is relative to the decoration's own (200%) box.
    const angle = finite(d.angle, 0.5), x = finite(d.offset_x, 0.25), y = finite(d.offset_y, 0.25)
    return {
      key: `${index}:${d.id}:${url}`,
      url,
      style: {
        rotate: angle === 0 ? undefined : `${angle * 360}deg`,
        scale: d.flip_h ? '-1 1' : undefined,
        translate: x === 0 && y === 0 ? undefined : `${x * 100}% ${y * 100}%`,
      } as Record<string, string | undefined>,
    }
  }).filter((d): d is NonNullable<typeof d> => d !== null)
})
const failedDecorations = ref<string[]>([])
function decorationFailed(key: string) {
  if (!failedDecorations.value.includes(key)) failedDecorations.value = [...failedDecorations.value, key]
}
const avatarSize = computed(() => props.size || 40)
const onlineStatus = computed(() => {
  const status = props.account.online_status
  return props.showOnline && (status === 'online' || status === 'active' || status === 'offline') ? status : ''
})
function avatarUrl(raw: string | undefined): string {
  if (!raw) return ''
  try {
    const url = new URL(raw, window.location.origin)
    // Old cached API responses used the same brand logo for every account.
    if (url.pathname === '/default-assets/avatar.png') return ''
    if (/^\/api\/v1\/avatars\/default\/[a-f0-9]{64}\.png$/.test(url.pathname)) return url.pathname
  } catch { /* The existing media URL policy handles other values. */ }
  return withCacheSize(sameOriginMediaUrl(raw))
}
// Ask the server's avatar cache for a rendition matching the rendered size
// (48/96/192/400 buckets), instead of downloading the full original.
const SIZE_BUCKETS = [48, 96, 192, 400]
function withCacheSize(url: string): string {
  if (!url || !/^\/(proxy\/media|media)\//.test(url)) return url
  const want = avatarSize.value * Math.min(Math.max(window.devicePixelRatio || 1, 1), 2)
  const bucket = SIZE_BUCKETS.find(s => s >= want) ?? 400
  return `${url}${url.includes('?') ? '&' : '?'}size=${bucket}`
}
// Lists, timelines and search show avatar_static (a small first-frame
// still); the animated original only plays where asked for, or while the
// pointer is over the avatar, and never with prefers-reduced-motion.
const reducedMotion = typeof window !== 'undefined' && !!window.matchMedia?.('(prefers-reduced-motion: reduce)').matches
const hoverAnimated = ref(false)
const stillUrl = computed(() => avatarUrl(props.account.avatar_static || props.account.avatar))
const animatedUrl = computed(() => avatarUrl(props.account.avatar))
const primaryUrl = computed(() => (!reducedMotion && (props.animated || hoverAnimated.value)) ? animatedUrl.value : stillUrl.value)
let hoverToken = 0
function hoverStart(event: PointerEvent) {
  if (reducedMotion || props.animated || event.pointerType !== 'mouse' || animatedUrl.value === stillUrl.value) return
  const token = ++hoverToken
  const url = animatedUrl.value
  // Swap only once the animated file is ready, so the still never blanks.
  const probe = new Image()
  probe.onload = () => { if (token === hoverToken) hoverAnimated.value = true }
  probe.src = url
}
function hoverEnd() { hoverToken++; hoverAnimated.value = false }
const defaultUrl = computed(() => avatarUrl(props.account.avatar_default))
const failedUrls = ref<string[]>([])
watch(() => [props.account.id, primaryUrl.value, defaultUrl.value], () => {
  failedUrls.value = []
}, { flush: 'sync' })
// A still that fails (e.g. an old server without ?static) falls back to the original.
const imageUrl = computed(() => [primaryUrl.value, animatedUrl.value, defaultUrl.value].find(url => url && !failedUrls.value.includes(url)) || '')
const pattern = computed(() => identiconPattern(avatarIdentity(props.account), defaultUrl.value || primaryUrl.value))
// Loading state: a shimmer in the avatar's own shape until the image has
// decoded, then a short fade. Images the browser already has (complete on
// mount) skip both, so cached avatars never flash.
const loadedUrl = ref('')
const instant = ref(false)
const imageEl = ref<HTMLImageElement | null>(null)
const loading = computed(() => !!imageUrl.value && loadedUrl.value !== imageUrl.value)
function checkComplete() {
  const img = imageEl.value
  if (img && img.complete && img.naturalWidth > 0 && img.getAttribute('src') === imageUrl.value) {
    instant.value = true
    loadedUrl.value = imageUrl.value
  }
}
watch(imageUrl, () => { instant.value = false; void nextTick(checkComplete) }, { immediate: true, flush: 'post' })
function imageLoaded(event: Event) {
  const src = (event.target as HTMLImageElement).getAttribute('src')
  if (src && src === imageUrl.value) loadedUrl.value = src
}
function imageFailed(event: Event) {
  // A queued error for the previous URL must not hide a replacement avatar.
  const failedUrl = (event.target as HTMLImageElement).getAttribute('src')
  if (failedUrl && failedUrl === imageUrl.value) failedUrls.value = [...failedUrls.value, failedUrl]
}
</script>

<template>
  <span class="avatar-frame" :style="{ width: `${avatarSize}px`, height: `${avatarSize}px` }" @pointerenter="hoverStart" @pointerleave="hoverEnd">
    <svg v-if="account.is_cat" class="avatar-cat-ears" viewBox="0 0 100 60" aria-hidden="true" focusable="false">
      <path class="avatar-cat-ears__fur" d="M7 54 C4 35 3 10 10 3 C21 6 34 22 39 37 Z M61 37 C66 22 79 6 90 3 C97 10 96 35 93 54 Z" />
      <path class="avatar-cat-ears__inner" d="M13 40 Q9 20 12 13 Q24 22 29 34 Z M71 34 Q76 22 88 13 Q91 20 87 40 Z" />
    </svg>
    <span
      class="user-avatar t-avatar"
      :style="{ fontSize: `${avatarSize / 2}px` }"
      role="img"
      :aria-label="`${account.display_name || account.username}'s avatar`"
      :class="{ 'user-avatar--loading': loading }"
      :aria-busy="loading ? 'true' : undefined"
    >
      <img
        v-if="imageUrl"
        :key="imageUrl"
        ref="imageEl"
        class="user-avatar__image"
        :class="{ 'is-loaded': !loading, 'is-instant': instant }"
        :src="imageUrl"
        alt=""
        aria-hidden="true"
        decoding="async"
        @load="imageLoaded"
        @error="imageFailed"
      />
      <svg v-else class="user-avatar__identicon" viewBox="0 0 128 128" aria-hidden="true" focusable="false">
        <rect width="128" height="128" :fill="pattern.background" />
        <rect v-for="cell in pattern.cells" :key="`${cell.x}-${cell.y}`" :x="cell.x" :y="cell.y" width="20" height="20" :fill="pattern.foreground" />
      </svg>
    </span>
    <img
      v-for="d in decorations"
      :key="d.key"
      class="avatar-decoration"
      :src="d.url"
      :style="failedDecorations.includes(d.key) ? { ...d.style, visibility: 'hidden' } : d.style"
      alt=""
      aria-hidden="true"
      draggable="false"
      @error="decorationFailed(d.key)"
    />
  <span v-if="onlineStatus" class="avatar-online" :class="`avatar-online--${onlineStatus}`" role="img" :aria-label="$t(`user.onlineStatus.${onlineStatus}`)" :title="$t(`user.onlineStatus.${onlineStatus}`)" data-testid="online-indicator" />
  </span>
</template>

<style scoped>
.avatar-frame { display: inline-flex; position: relative; flex-shrink: 0; vertical-align: middle; }
/* Original vector artwork for Community cat avatars. */
.avatar-online { position: absolute; z-index: 2; right: 4%; bottom: 4%; width: 18%; height: 18%; min-width: 10px; min-height: 10px; border-radius: 50%; border: 2px solid var(--td-bg-color-container, #fff); box-sizing: border-box; }
.avatar-online--online { background: var(--td-success-color, #2ba471); }
.avatar-online--active { background: var(--td-warning-color, #e37318); }
.avatar-online--offline { background: var(--td-error-color, #d54941); }
.avatar-cat-ears { position: absolute; top: -17%; left: 0; width: 100%; height: 60%; overflow: visible; pointer-events: none; }
.avatar-cat-ears__fur { fill: #f0e9e3; stroke: #938c91; stroke-width: 2; stroke-linejoin: round; }
.avatar-cat-ears__inner { fill: #e8a2b7; }
/* Misskey MkAvatar geometry: 200% of the avatar, centred on it. */
.avatar-decoration { position: absolute; z-index: 1; top: -50%; left: -50%; width: 200%; max-width: none; pointer-events: none; user-select: none; }
.user-avatar {
  position: relative;
  display: inline-flex;
  align-items: center;
  justify-content: center;
  width: 100%;
  height: 100%;
  flex-shrink: 0;
  overflow: hidden;
  white-space: nowrap;
  border-radius: var(--td-radius-circle, 50%);
  background: var(--color-fill);
  color: var(--color-label-secondary);
  font: var(--td-font-body-medium);
  font-weight: var(--weight-semibold);
  line-height: 1;
  cursor: pointer;
}
.user-avatar__identicon { display: block; width: 100%; height: 100%; }
.user-avatar__image { display: block; width: 100%; height: 100%; object-fit: cover; opacity: 0; transition: opacity .2s ease-out; }
.user-avatar__image.is-loaded { opacity: 1; }
.user-avatar__image.is-instant { transition: none; }
/* Shimmer placeholder in the avatar's own circle; no layout change. */
.user-avatar--loading::before {
  content: "";
  position: absolute;
  inset: 0;
  background: linear-gradient(100deg, transparent 20%, color-mix(in srgb, var(--color-label) 9%, transparent) 50%, transparent 80%) var(--color-fill);
  background-size: 220% 100%;
  animation: user-avatar-shimmer 1.2s ease-in-out infinite;
}
@keyframes user-avatar-shimmer { from { background-position: 120% 0; } to { background-position: -120% 0; } }
@media (prefers-reduced-motion: reduce) {
  .user-avatar__image { transition: none; }
  .user-avatar--loading::before { animation: none; background: var(--color-fill); }
}
.user-avatar:focus-visible { outline: 2px solid var(--color-focus-ring); outline-offset: 2px; }
</style>
