<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'

const props = defineProps<{
  account: MastodonAccount
  size?: number
}>()

const decoration = computed(() => /^\/api\/v1\/avatar_decorations\/\d+\/image$/.test(props.account.avatar_decoration_url || '') ? props.account.avatar_decoration_url : '')
const avatarSize = computed(() => props.size || 40)
const imageUrl = computed(() => sameOriginMediaUrl(props.account.avatar))
const failed = ref(false)
watch(imageUrl, () => { failed.value = false }, { flush: 'sync' })
const initial = computed(() => props.account.display_name?.charAt(0) || props.account.username?.charAt(0) || 'U')
function imageFailed(event: Event) {
  // A queued error for the previous URL must not hide a replacement avatar.
  if ((event.target as HTMLImageElement).getAttribute('src') === imageUrl.value) failed.value = true
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
      <img v-if="imageUrl && !failed" :key="imageUrl" class="user-avatar__image" :src="imageUrl" alt="" aria-hidden="true" @error="imageFailed" />
      <span v-else aria-hidden="true">{{ initial }}</span>
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
.user-avatar__image { display: block; width: 100%; height: 100%; object-fit: cover; }
.user-avatar:focus-visible { outline: 2px solid var(--color-focus-ring); outline-offset: 2px; }
</style>
