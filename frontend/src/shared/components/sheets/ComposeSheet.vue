<script setup lang="ts">
import { ref, watch } from 'vue'
import { MessagePlugin } from '@/shared/ui/feedback'
import BottomSheet from './BottomSheet.vue'
import PostComposer from '@/shared/components/post/PostComposer.vue'
import type { MastodonStatus } from '@/shared/types/mastodon'
import { useTimelineStore } from '@/domains/timeline/presentation/stores/timelineStore'
import { useUiChrome } from '@/shared/composables/useUiChrome'

import { t } from '@/shared/i18n'
const { composeOpen, quoteTarget } = useUiChrome()
const timelineStore = useTimelineStore()

// Mount a fresh composer each time the sheet opens, but keep it rendered
// until the exit animation has finished so the sheet doesn't collapse
// while it slides away.
const composerMounted = ref(composeOpen.value)
watch(composeOpen, (open) => { if (open) composerMounted.value = true })

// The composer posts by itself (text, CW, poll, media, visibility) and
// emits the created status.
function handlePosted(status: MastodonStatus) {
  timelineStore.prependStatus(status)
  composeOpen.value = false
  quoteTarget.value = null
  MessagePlugin.success(t('shared.ComposeSheet.s2'))
}
</script>

<template>
  <BottomSheet v-model="composeOpen" :title="$t('shared.ComposeSheet.s1')" size="large" @closed="composerMounted = false">
    <div class="compose-sheet">
      <PostComposer v-if="composerMounted" :quote-to="quoteTarget" autofocus @posted="handlePosted" />
    </div>
  </BottomSheet>
</template>

<style scoped>
.compose-sheet {
  margin: 0 calc(-1 * var(--space-4));
}

.compose-sheet :deep(.composer) {
  box-shadow: none;
  padding-top: var(--space-2);
}

.compose-sheet :deep(.composer__textarea:not(.composer__textarea--compact) .t-textarea__inner) {
  min-height: 8rem !important;
}
</style>
