<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { AvatarDecorationPlacement, MastodonAccount } from '@/shared/types/mastodon'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'
import { avatarIdentity, identiconPattern } from '@/shared/utils/accountAvatar'

const props = defineProps<{
  account: MastodonAccount
  size?: number
  /** Overrides the account's decorations (settings preview). */
  decorations?: AvatarDecorationPlacement[]
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
const primaryUrl = computed(() => avatarUrl(props.account.avatar))
const defaultUrl = computed(() => avatarUrl(props.account.avatar_default))
const failedUrls = ref<string[]>([])
watch(() => [props.account.id, primaryUrl.value, defaultUrl.value], () => {
  failedUrls.value = []
}, { flush: 'sync' })
const imageUrl = computed(() => [primaryUrl.value, defaultUrl.value].find(url => url && !failedUrls.value.includes(url)) || '')
const pattern = computed(() => identiconPattern(avatarIdentity(props.account), defaultUrl.value || primaryUrl.value))
function imageFailed(event: Event) {
  // A queued error for the previous URL must not hide a replacement avatar.
  const failedUrl = (event.target as HTMLImageElement).getAttribute('src')
  if (failedUrl && failedUrl === imageUrl.value) failedUrls.value = [...failedUrls.value, failedUrl]
}
</script>

<template>
  <span class="avatar-frame" :style="{ width: `${avatarSize}px`, height: `${avatarSize}px` }">
    <span
      class="user-avatar t-avatar"
      :style="{ fontSize: `${avatarSize / 2}px` }"
      role="img"
      :aria-label="`${account.display_name || account.username}'s avatar`"
    >
      <img v-if="imageUrl" :key="imageUrl" class="user-avatar__image" :src="imageUrl" alt="" aria-hidden="true" @error="imageFailed" />
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
  </span>
</template>

<style scoped>
.avatar-frame { display: inline-flex; position: relative; flex-shrink: 0; vertical-align: middle; }
/* Misskey MkAvatar geometry: 200% of the avatar, centred on it. */
.avatar-decoration { position: absolute; z-index: 1; top: -50%; left: -50%; width: 200%; max-width: none; pointer-events: none; user-select: none; }
.user-avatar {
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
.user-avatar__image { display: block; width: 100%; height: 100%; object-fit: cover; }
.user-avatar:focus-visible { outline: 2px solid var(--color-focus-ring); outline-offset: 2px; }
</style>
