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
    <span v-else class="reaction-label__text">{{ reaction.name }}</span>
    <button v-if="reaction.is_sensitive && !revealed" type="button" class="reaction-label__reveal" @click.stop="revealed = true" @keydown.enter.stop>{{ $t('shared.ReactionLabel.s1') }}</button>
  </span>
</template>
<style scoped>
.reaction-label { display: inline-flex; align-items: center; flex-wrap: wrap; gap: 6px; min-width: 0; max-width: 100%; vertical-align: middle; }
.reaction-label img { display: inline-block; width: 1.3em; height: 1.3em; flex-shrink: 0; object-fit: contain; }
.reaction-label__text { overflow-wrap: anywhere; }
.reaction-label__reveal { padding: 3px 7px; border: 1px solid var(--color-separator); border-radius: 6px; background: var(--color-fill); color: var(--color-label-secondary); font-size: 11px; line-height: 1.4; cursor: pointer; }
.reaction-label__reveal:focus-visible { outline: 2px solid var(--color-accent); outline-offset: 2px; }
@media (hover: hover) { .reaction-label__reveal:hover { color: var(--color-accent-text); background: var(--color-accent-soft); } }
</style>
