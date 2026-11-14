<script setup lang="ts">
import { ref, watch } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import BottomSheet from './BottomSheet.vue'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import ReplyContext from '@/shared/components/post/ReplyContext.vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useTimelineStore } from '@/domains/timeline/presentation/stores/timelineStore'
import { incrementReplyCount } from '@/domains/post/presentation/composables/useStatusActions'
import { useUiChrome } from '@/shared/composables/useUiChrome'

import { t } from '@/shared/i18n'
const { composeOpen, quoteTarget, replyTarget } = useUiChrome()
const timelineStore = useTimelineStore()

// Mount a fresh composer each time the sheet opens, but keep it rendered
// until the exit animation has finished so the sheet doesn't collapse
// while it slides away.
const composerMounted = ref(composeOpen.value)
watch(composeOpen, (open) => { if (open) composerMounted.value = true })

function handleSheetClosed() {
  composerMounted.value = false
  if (!composeOpen.value) {
    quoteTarget.value = null
    replyTarget.value = null
  }
}

// The composer posts by itself (text, CW, poll, media, visibility) and
// emits the created status.
function handlePosted(status: MastodonStatus) {
  const wasReply = replyTarget.value
  if (wasReply) {
    incrementReplyCount(wasReply)
    timelineStore.prependStatus(status, { reveal: false })
  } else {
    timelineStore.prependStatus(status)
  }
  composeOpen.value = false
  quoteTarget.value = null
  replyTarget.value = null
  MessagePlugin.success(t(wasReply ? 'shared.ComposeSheet.replyPosted' : 'shared.ComposeSheet.s2'))
}
</script>

<template>
  <BottomSheet
    v-model="composeOpen"
    :title="replyTarget ? $t('shared.ComposeSheet.replyTitle') : $t('shared.ComposeSheet.s1')"
    size="large"
    @closed="handleSheetClosed"
  >
    <div class="compose-sheet">
      <div v-if="composerMounted && replyTarget" class="compose-sheet__reply-context">
        <ReplyContext :status="replyTarget" />
      </div>
      <PostComposer
        v-if="composerMounted"
        :key="replyTarget ? `reply:${replyTarget.id}` : quoteTarget ? `quote:${quoteTarget.id}` : 'compose'"
        :reply-to="replyTarget"
        :quote-to="quoteTarget"
        autofocus
        @posted="handlePosted"
      />
    </div>
  </BottomSheet>
</template>

<style scoped>
.compose-sheet {
  margin: 0 calc(-1 * var(--space-4));
}

.compose-sheet__reply-context {
  padding: var(--space-2) var(--gutter) 0;
}

.compose-sheet :deep(.composer) {
  box-shadow: none;
  padding-top: var(--space-2);
}

.compose-sheet :deep(.composer__textarea:not(.composer__textarea--compact) .t-textarea__inner) {
  min-height: 8rem !important;
}
</style>
