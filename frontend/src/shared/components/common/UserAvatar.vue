<script setup lang="ts">
import { computed } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'
import { sameOriginMediaUrl } from '@/shared/utils/mediaUrl'

const props = defineProps<{
  account: MastodonAccount
  size?: number
}>()

const avatarSize = computed(() => props.size || 40)
</script>

<template>
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
</template>

<style scoped>
.user-avatar {
  flex-shrink: 0;
  cursor: pointer;
}

.user-avatar:focus-visible {
  outline: 2px solid var(--color-focus-ring);
  outline-offset: 2px;
}
</style>
