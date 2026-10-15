<script setup lang="ts">
import { computed, ref, watch } from 'vue'
import type { MastodonCard } from '@/shared/types/mastodon'
import { safeHttpLink } from '@/shared/utils/links'
import ProgressiveImage from './ProgressiveImage.vue'

const props = defineProps<{ card: MastodonCard; hideImage?: boolean }>()
const url = computed(() => safeHttpLink(props.card.url))
const host = computed(() => url.value ? new URL(url.value).hostname.replace(/^www\./, '') : '')
const title = computed(() => props.card.title?.trim() || host.value)
const provider = computed(() => props.card.provider_name?.trim())
const image = computed(() => {
  if (props.hideImage || !props.card.image) return null
  try { return safeHttpLink(new URL(props.card.image, window.location.origin).href) }
  catch { return null }
})
const failedImage = ref(false)
watch(image, () => { failedImage.value = false }, { flush: 'sync' })
function imageFailed(event: Event) {
  if ((event.target as HTMLImageElement).getAttribute('src') === image.value) failedImage.value = true
}
</script>

<template>
  <a v-if="url" class="link-preview" :href="url" target="_blank" rel="nofollow noopener noreferrer" @click.stop>
    <div class="link-preview__copy">
      <span class="link-preview__site">
        <svg viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="m8 12 4-4M7 14H5a4 4 0 0 1 0-8h2m6 0h2a4 4 0 0 1 0 8h-2" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" /></svg>
        <span>{{ host }}</span>
        <svg class="link-preview__external" viewBox="0 0 20 20" fill="none" aria-hidden="true"><path d="M7 13 14 6M7 6h7v7" stroke="currentColor" stroke-width="1.5" stroke-linecap="round" stroke-linejoin="round" /></svg>
      </span>
      <strong class="link-preview__title" dir="auto">{{ title }}</strong>
      <span v-if="card.description" class="link-preview__description" dir="auto">{{ card.description }}</span>
      <span v-if="provider && provider.toLowerCase() !== host.toLowerCase()" class="link-preview__provider" dir="auto">{{ provider }}</span>
    </div>
    <ProgressiveImage v-if="image && !failedImage" :key="image" class="link-preview__image" :src="image" :blurhash="card.blurhash" referrerpolicy="no-referrer" @error="imageFailed" />
  </a>
</template>

<style scoped>
.link-preview {
  display: flex;
  align-items: stretch;
  min-width: 0;
  max-width: 100%;
  margin-top: var(--space-3);
  overflow: hidden;
  border: 1px solid var(--color-separator);
  border-radius: var(--radius-lg);
  background: var(--color-bg);
  color: var(--color-label);
  text-decoration: none;
}
.link-preview__copy { display: flex; flex: 1; flex-direction: column; gap: 5px; min-width: 0; padding: 13px 14px; }
.link-preview__site { display: flex; align-items: center; gap: 6px; min-width: 0; color: var(--color-label-secondary); font-size: var(--text-caption); }
.link-preview__site svg { width: 15px; height: 15px; flex-shrink: 0; }
.link-preview__site > span { overflow: hidden; white-space: nowrap; text-overflow: ellipsis; }
.link-preview__site .link-preview__external { width: 14px; height: 14px; margin-left: auto; color: var(--color-label-tertiary); }
.link-preview__title { font-size: var(--text-subhead); font-weight: var(--weight-semibold); line-height: 1.45; }
.link-preview__description { font-size: var(--text-footnote); line-height: 1.5; color: var(--color-label-secondary); }
.link-preview__title, .link-preview__description { display: -webkit-box; -webkit-box-orient: vertical; -webkit-line-clamp: 2; overflow: hidden; overflow-wrap: anywhere; }
.link-preview__provider { overflow: hidden; text-overflow: ellipsis; white-space: nowrap; font-size: var(--text-caption); color: var(--color-label-tertiary); }
.link-preview__image { align-self: center; flex-shrink: 0; width: 104px; height: 104px; margin: 12px 12px 12px 0; border-radius: var(--radius-md); overflow: hidden; background: var(--color-fill); }
.link-preview:focus-visible { outline: 2px solid var(--color-focus-ring); outline-offset: 3px; }
.link-preview:active { background: var(--color-fill); }
@media (hover: hover) and (pointer: fine) { .link-preview:hover { background: var(--color-bg-row); } }
@media (max-width: 480px) {
  .link-preview__copy { padding: 11px 12px; }
  .link-preview__image { width: 68px; height: 80px; margin: 10px 10px 10px 0; }
}
</style>
