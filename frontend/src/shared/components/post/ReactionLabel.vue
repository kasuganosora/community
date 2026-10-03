<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { MastodonEmojiReaction } from '@/shared/types/mastodon'
import { emojiUrl } from '@/domains/emoji/domain/emoji'
const props = defineProps<{ reaction: MastodonEmojiReaction }>()
const revealed = ref(false)
watch(() => props.reaction, () => { revealed.value = false })
const src = computed(() => {
  if (props.reaction.is_sensitive && !revealed.value) return null
  return emojiUrl({ shortcode: props.reaction.name, url: props.reaction.url || '', static_url: props.reaction.static_url || '', visible_in_picker: false })
})
</script>
<template>
  <span class="reaction-label">
    <img v-if="src" :src="src" :alt="reaction.name" loading="lazy" decoding="async" referrerpolicy="no-referrer" />
    <template v-else>{{ reaction.name }}</template>
    <button v-if="reaction.is_sensitive && !revealed" type="button" class="btn btn--ghost btn--sm" @click.stop="revealed = true" @keydown.enter.stop>显示敏感表情</button>
  </span>
</template>
<style scoped>
.reaction-label img { width: 1.3em; height: 1.3em; object-fit: contain; vertical-align: -0.25em; }
</style>
