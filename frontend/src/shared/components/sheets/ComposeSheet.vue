<script setup lang="ts">
import { MessagePlugin } from 'tdesign-vue-next'
import BottomSheet from './BottomSheet.vue'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useTimelineStore } from '@/domains/timeline/presentation/stores/timelineStore'
import { useUiChrome } from '@/shared/composables/useUiChrome'

const { composeOpen } = useUiChrome()
const timelineStore = useTimelineStore()

// The composer posts by itself (text, CW, poll, media, visibility) and
// emits the created status.
function handlePosted(status: MastodonStatus) {
  timelineStore.prependStatus(status)
  composeOpen.value = false
  MessagePlugin.success('已发布')
}
</script>

<template>
  <BottomSheet v-model="composeOpen" title="新帖子" size="large">
    <div class="compose-sheet">
      <PostComposer v-if="composeOpen" autofocus @posted="handlePosted" />
    </div>
  </BottomSheet>
</template>

<style scoped>
.compose-sheet {
  margin: 0 calc(-1 * var(--space-4));
}

.compose-sheet :deep(.composer) {
  border-bottom: none;
  padding-top: var(--space-2);
}

.compose-sheet :deep(.t-textarea__inner) {
  min-height: 8rem !important;
}
</style>
