<script setup lang="ts">
import { computed } from 'vue'
import type { MastodonAttachment } from '@/shared/types/mastodon'

const props = defineProps<{
  attachments: MastodonAttachment[]
}>()

const images = computed(() =>
  props.attachments.filter(a => a.type === 'image')
)

const gridClass = computed(() => {
  const count = images.value.length
  if (count === 1) return 'media-grid--single'
  if (count === 2) return 'media-grid--double'
  if (count === 3) return 'media-grid--triple'
  return 'media-grid--quad'
})
</script>

<template>
  <div class="post-media" :class="gridClass" v-if="images.length">
    <div
      v-for="(img, index) in images.slice(0, 4)"
      :key="img.id"
      class="media-item"
    >
      <img
        :src="img.preview_url || img.url"
        :alt="img.description || ''"
        loading="lazy"
        class="media-img"
      />
      <div v-if="index === 3 && images.length > 4" class="media-overflow">
        +{{ images.length - 4 }}
      </div>
    </div>
  </div>
</template>

<style scoped>
.post-media {
  display: grid;
  gap: 2px;
  border-radius: var(--radius-lg);
  overflow: hidden;
  margin-top: var(--space-3);
  max-height: 510px;
}

.media-grid--single {
  grid-template-columns: 1fr;
}

.media-grid--double {
  grid-template-columns: 1fr 1fr;
}

.media-grid--triple {
  grid-template-columns: 1fr 1fr;
  grid-template-rows: 1fr 1fr;
}

.media-grid--triple .media-item:first-child {
  grid-row: span 2;
}

.media-grid--quad {
  grid-template-columns: 1fr 1fr;
  grid-template-rows: 1fr 1fr;
}

.media-item {
  position: relative;
  overflow: hidden;
  cursor: pointer;
  min-height: 0;
}

.media-img {
  width: 100%;
  height: 100%;
  object-fit: cover;
}

.media-overflow {
  position: absolute;
  inset: 0;
  background: rgba(0, 0, 0, 0.5);
  display: flex;
  align-items: center;
  justify-content: center;
  font-size: var(--font-2xl);
  font-weight: 700;
  color: white;
}
</style>
