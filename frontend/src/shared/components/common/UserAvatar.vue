<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'
import { avatarIdentity, identiconPattern } from '@/shared/utils/accountAvatar'

const props = defineProps<{
  account: MastodonAccount
  size?: number
}>()

const decoration = computed(() => /^\/api\/v1\/avatar_decorations\/\d+\/image$/.test(props.account.avatar_decoration_url || '') ? props.account.avatar_decoration_url : '')
const avatarSize = computed(() => props.size || 40)
function avatarUrl(raw: string | undefined): string {
  if (!raw) return ''
  try {
    const url = new URL(raw, window.location.origin)
    // Old cached API responses used the same brand logo for every account.
    if (url.pathname === '/default-assets/avatar.png') return ''
    if (/^\/api\/v1\/avatars\/default\/[a-f0-9]{64}\.png$/.test(url.pathname)) return url.pathname
  } catch { /* The existing media URL policy handles other values. */ }
  return sameOriginMediaUrl(raw)
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
    <img v-if="decoration" :key="decoration" class="avatar-decoration" :src="decoration" alt="" aria-hidden="true" @error="($event.target as HTMLImageElement).style.visibility = 'hidden'" />
  </span>
</template>

<style scoped>
.avatar-frame { display: inline-flex; position: relative; flex-shrink: 0; vertical-align: middle; }
.avatar-decoration { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: contain; pointer-events: none; }
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
