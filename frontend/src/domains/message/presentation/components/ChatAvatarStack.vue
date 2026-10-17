<script setup lang="ts">
import UserAvatar from '@/shared/components/common/UserAvatar.vue'
import type { MastodonAccount } from '@/shared/types/mastodon'

// One avatar for a 1:1 chat, a small overlapping stack for groups, the chat
// icon when nobody else is known yet.
defineProps<{ accounts: MastodonAccount[]; size?: number }>()
</script>

<template>
  <span v-if="accounts.length === 1" class="chat-avatars" aria-hidden="true">
    <UserAvatar :account="accounts[0]" :size="size ?? 44" />
  </span>
  <span v-else-if="accounts.length > 1" class="chat-avatars chat-avatars--stack" :class="`chat-avatars--${accounts.length}`" :style="{ width: `${size ?? 44}px`, height: `${size ?? 44}px` }" aria-hidden="true">
    <span v-for="(a, i) in accounts" :key="a.id" class="chat-avatars__item" :class="`chat-avatars__item--${i}`">
      <UserAvatar :account="a" :size="Math.round((size ?? 44) * (accounts.length === 2 ? 0.68 : 0.58))" />
    </span>
  </span>
  <span v-else class="chat-avatars chat-avatars--fallback" :style="{ width: `${size ?? 44}px`, height: `${size ?? 44}px` }" aria-hidden="true">
    <t-icon name="chat" :size="20" />
  </span>
</template>

<style scoped>
.chat-avatars { position: relative; display: inline-flex; flex-shrink: 0; }
.chat-avatars--fallback { align-items: center; justify-content: center; border-radius: 50%; background: var(--color-fill); color: var(--color-label-secondary); }
.chat-avatars__item { position: absolute; display: inline-flex; border-radius: 50%; box-shadow: 0 0 0 2px var(--color-bg, #fff); }
.chat-avatars--2 .chat-avatars__item--0 { top: 0; left: 0; }
.chat-avatars--2 .chat-avatars__item--1 { right: 0; bottom: 0; }
.chat-avatars--3 .chat-avatars__item--0 { top: 0; left: 50%; transform: translateX(-50%); }
.chat-avatars--3 .chat-avatars__item--1 { bottom: 0; left: 0; }
.chat-avatars--3 .chat-avatars__item--2 { right: 0; bottom: 0; }
</style>
