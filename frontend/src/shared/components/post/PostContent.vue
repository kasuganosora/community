<script setup lang="ts">
/**
 * Body of a status: content warning, text, media and poll. Shared by the
 * timeline card and the status detail page.
 */
import { ref, computed, watch } from 'vue'
import type { MastodonPoll, MastodonStatus } from '@/shared/types/mastodon'
import { useAuthStore } from '@/domains/auth/presentation/stores/authStore'
import RichText from '@/shared/components/common/RichText.vue'
import PostMedia from './PostMedia.vue'
import PostPoll from './PostPoll.vue'

const props = defineProps<{
  status: MastodonStatus
  textClass?: string
}>()

const authStore = useAuthStore()
const cwOpen = ref(false)
const mediaRevealed = ref(false)
const pollOverride = ref<MastodonPoll | null>(null)

watch(() => props.status.id, () => {
  cwOpen.value = false
  mediaRevealed.value = false
  pollOverride.value = null
})

const hasCw = computed(() => !!props.status.spoiler_text)
const bodyVisible = computed(() => !hasCw.value || cwOpen.value)
const poll = computed(() => pollOverride.value ?? props.status.poll)
const isOwn = computed(() => !!authStore.currentUser && authStore.currentUser.id === props.status.account.id)
// Media marked sensitive without a CW still needs a click to reveal.
const mediaHidden = computed(() => props.status.sensitive && !hasCw.value && !mediaRevealed.value)
const contentId = computed(() => `status-content-${props.status.id}-${Math.random().toString(36).slice(2, 6)}`)
</script>

<template>
  <div class="post-content">
    <div v-if="hasCw" class="post-content__cw">
      <span class="post-content__cw-text">{{ status.spoiler_text }}</span>
      <button
        type="button"
        class="post-content__cw-toggle"
        :aria-expanded="cwOpen"
        :aria-controls="contentId"
        @click.stop="cwOpen = !cwOpen"
      >
        {{ cwOpen ? '收起' : '显示内容' }}
      </button>
    </div>
    <div v-show="bodyVisible" :id="contentId">
      <RichText :html="status.content" :class="textClass" @click.stop />
      <div v-if="status.media_attachments?.length" class="post-content__media">
        <PostMedia v-if="!mediaHidden" :attachments="status.media_attachments" />
        <button
          v-else
          type="button"
          class="post-content__sensitive"
          @click.stop="mediaRevealed = true"
        >
          敏感内容，点击查看
        </button>
      </div>
      <PostPoll v-if="poll" :poll="poll" :own="isOwn" @updated="pollOverride = $event" />
    </div>
  </div>
</template>

<style scoped>
.post-content__cw {
  display: flex;
  flex-wrap: wrap;
  align-items: center;
  gap: var(--space-2);
  margin: var(--space-1) 0;
}

.post-content__cw-text {
  font-weight: var(--font-medium, 500);
}

.post-content__cw-toggle {
  font-size: var(--font-xs);
  padding: 2px var(--space-2);
  border-radius: var(--radius-sm, 4px);
  background: var(--bg-hover);
  color: var(--text-primary);
}

.post-content__sensitive {
  width: 100%;
  padding: var(--space-6, 24px) var(--space-4);
  margin-top: var(--space-2);
  border-radius: var(--radius-lg, 12px);
  background: var(--bg-secondary);
  color: var(--text-secondary);
}
</style>
