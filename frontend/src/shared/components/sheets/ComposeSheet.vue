<script setup lang="ts">
import { ref } from 'vue'
import { MessagePlugin } from 'tdesign-vue-next'
import BottomSheet from './BottomSheet.vue'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import { postApi } from '@/domains/post/infrastructure/postApi'
import { useTimelineStore } from '@/domains/timeline/presentation/stores/timelineStore'
import { useUiChrome } from '@/shared/composables/useUiChrome'

const { composeOpen } = useUiChrome()
const timelineStore = useTimelineStore()
const composerRef = ref<InstanceType<typeof PostComposer>>()

// Same flow as the inline composer on the home timeline.
async function handlePosted(content: string) {
  try {
    const status = await postApi.create({ status: content, visibility: 'public' })
    timelineStore.prependStatus(status)
    composerRef.value?.onPostSuccess()
    composeOpen.value = false
    MessagePlugin.success('已发布')
  } catch (e) {
    console.error('Failed to create post:', e)
    composerRef.value?.onPostError('发布失败，请重试')
  }
}
</script>

<template>
  <BottomSheet v-model="composeOpen" title="新帖子" size="large">
    <div class="compose-sheet">
      <PostComposer ref="composerRef" @posted="handlePosted" />
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
