<script setup lang="ts">
import { computed } from 'vue'
import type { MastodonAccount } from '@/shared/types/mastodon'

const props = defineProps<{
  account: MastodonAccount
  size?: number
}>()

const avatarSize = computed(() => props.size || 40)
</script>

<template>
  <t-avatar
    :size="`${avatarSize}px`"
    :image="account.avatar || undefined"
    shape="circle"
    class="user-avatar"
  >
    {{ account.display_name?.charAt(0) || account.username?.charAt(0) || 'U' }}
  </t-avatar>
</template>

<style scoped>
.user-avatar {
  flex-shrink: 0;
  cursor: pointer;
}
</style>
