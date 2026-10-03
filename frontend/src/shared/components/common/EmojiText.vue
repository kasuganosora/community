<script setup lang="ts">
import { computed } from 'vue'
import { emojiMap, emojiUrl } from '@/domains/emoji/domain/emoji'
import type { MastodonEmoji } from '@/shared/types/mastodon'
const props = defineProps<{ text: string; emojis?: MastodonEmoji[] }>()
const parts = computed(() => {
  const map = emojiMap(props.emojis)
  return props.text.split(/(:[a-zA-Z0-9_+-]+:)/g).map(text => {
    const emoji = text.startsWith(':') && text.endsWith(':') ? map.get(text.slice(1, -1)) : undefined
    return { text, src: emoji && !emoji.is_sensitive ? emojiUrl(emoji) : null }
  })
})
</script>
<template>
  <span><template v-for="(part, index) in parts" :key="index"><img v-if="part.src" class="custom-emoji" :src="part.src" :alt="part.text" :title="part.text" loading="lazy" decoding="async" referrerpolicy="no-referrer" /><template v-else>{{ part.text }}</template></template></span>
</template>
<style scoped>
.custom-emoji { width: 1.3em; height: 1.3em; object-fit: contain; vertical-align: -0.25em; }
</style>
