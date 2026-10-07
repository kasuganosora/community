<script setup lang="ts">
import { computed } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'

const props = defineProps<{
  account: MastodonAccount
  size?: number
}>()

const decoration = computed(() => /^\/api\/v1\/avatar_decorations\/\d+\/image$/.test(props.account.avatar_decoration_url || '') ? props.account.avatar_decoration_url : '')
const avatarSize = computed(() => props.size || 40)
</script>

<template>
 <span class="avatar-frame" :style="{ width: `${avatarSize}px`, height: `${avatarSize}px` }">
  <t-avatar
    :size="`${avatarSize}px`"
    :image="sameOriginMediaUrl(account.avatar) || undefined"
    shape="circle"
    class="user-avatar"
    role="img"
    :aria-label="`${account.display_name || account.username}'s avatar`"
  >
    {{ account.display_name?.charAt(0) || account.username?.charAt(0) || 'U' }}
  </t-avatar>
  <img v-if="decoration" :key="decoration" class="avatar-decoration" :src="decoration" alt="" aria-hidden="true" @error="($event.target as HTMLImageElement).style.visibility = 'hidden'" />
 </span>
</template>

<style scoped>
.avatar-frame { display: inline-flex; position: relative; flex-shrink: 0; vertical-align: middle; }
.avatar-decoration { position: absolute; inset: 0; width: 100%; height: 100%; object-fit: contain; pointer-events: none; }
.user-avatar {
  flex-shrink: 0;
  cursor: pointer;
}

.user-avatar:focus-visible {
  outline: 2px solid var(--color-focus-ring);
  outline-offset: 2px;
}
</style>
